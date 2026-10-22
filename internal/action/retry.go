package action

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/dortanes/prox/internal/router"
)

// dial connects to target. When the dial fails, the balanced target is
// ejected and the next target of its group that this request has not tried
// is dialed instead, until the group is exhausted. It returns the target the
// connection went to, or the last one tried on error.
func (p *Proxy) dial(r *http.Request, target *url.URL) (net.Conn, *url.URL, error) {
	for {
		conn, err := dialUpstream(target, p.timeout)
		if err == nil {
			return conn, target, nil
		}
		next := p.retryTarget(r, target, err)
		if next == nil {
			return nil, target, err
		}
		target = next
	}
}

// retryTarget reports a failed dial to the route's balancer and returns the
// upstream to try next, or nil when there is none. Only a failure to connect
// qualifies: after it, nothing of the request has reached the upstream.
func (p *Proxy) retryTarget(r *http.Request, failed *url.URL, cause error) *url.URL {
	var opErr *net.OpError
	if p.upstreamTpl == "" || !errors.As(cause, &opErr) || opErr.Op != "dial" {
		return nil
	}
	match := router.GetMatchResult(r)
	if match.Retry(cause) == "" {
		return nil
	}
	next, err := parseUpstream(resolveTemplate(p.upstreamTpl, match))
	if err != nil {
		return nil
	}
	slog.Debug("upstream dial failed, trying next target",
		"failed", failed.Host,
		"next", next.Host,
		"err", cause,
	)
	return next
}

// retryTransport sends a request to the next target of its group when the
// dial to the chosen one fails.
type retryTransport struct {
	http.RoundTripper
	proxy *Proxy
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// A failed dial closes the request body; keep it open for the next attempt.
	var body *unsentBody
	if req.Body != nil && req.Body != http.NoBody {
		body = &unsentBody{ReadCloser: req.Body}
		req = req.Clone(req.Context())
		req.Body = body
	}
	for {
		resp, err := t.RoundTripper.RoundTrip(req)
		if err == nil || (body != nil && body.read.Load()) {
			return resp, err
		}
		next := t.proxy.retryTarget(req, req.URL, err)
		if next == nil {
			return resp, err
		}
		req = req.Clone(req.Context())
		req.URL.Scheme = next.Scheme
		req.URL.Host = next.Host
	}
}

// unsentBody records whether the transport started sending the body, and
// leaves closing it to the server, which closes it when the handler returns.
type unsentBody struct {
	io.ReadCloser
	read atomic.Bool
}

func (b *unsentBody) Read(p []byte) (int, error) {
	b.read.Store(true)
	return b.ReadCloser.Read(p)
}

func (b *unsentBody) Close() error { return nil }
