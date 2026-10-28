package tests

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dortanes/prox/internal/plugin"
)

const (
	connectSNI       = "a.example.test"
	connectAuthority = "target.example:443"
)

// TestConnect_RoutesBySNI checks that a CONNECT over TLS is routed by the SNI,
// not the CONNECT authority, over HTTP/1.1 and HTTP/2, on a listener served
// directly and on one behind the L4 dispatcher.
func TestConnect_RoutesBySNI(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	events := filepath.Join(dir, "events.jsonl")
	t.Setenv(probeEventsEnv, events)

	cert, certFile, keyFile := writeTestCert(t, dir)
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)
	directPort, dispatchedPort := freePort(t), freePort(t)
	directAddr := fmt.Sprintf("127.0.0.1:%d", directPort)
	dispatchedAddr := fmt.Sprintf("127.0.0.1:%d", dispatchedPort)

	cfg := fmt.Sprintf(`{
		plugins: { probe: { path: %q } },
		services: {
			direct: {
				listen: %q,
				tls: true,
				tls_cert: %q,
				tls_key: %q,
				plugins: ["probe"],
				routes: [
					{ match: { domain: "*.example.test", methods: ["CONNECT"] }, action: "connect" },
				],
			},
			dispatched: {
				listen: %q,
				tls: true,
				tls_cert: %q,
				tls_key: %q,
				plugins: ["probe"],
				routes: [
					{ match: { domain: "*.l4.test" }, action: "l4" },
					{ match: { domain: "*.example.test", methods: ["CONNECT"] }, action: "connect" },
				],
			},
		},
		actions: {
			connect: { type: "proxy", upstream: %q },
			l4: { type: "pass", upstream: %q },
		},
	}`,
		exe,
		directAddr, certFile, keyFile,
		dispatchedAddr, certFile, keyFile,
		connectEchoUpstream(t), tlsEchoUpstream(t, cert),
	)
	stop := startProx(t, writeConfig(t, cfg), directPort)
	t.Cleanup(stop)
	waitForPort(t, dispatchedPort, 3*time.Second)

	waitFor(t, "probe plugin ready", func() bool {
		_ = connectHTTP1(directAddr, roots, "ready")
		return connectRequest(t, events, "ready") != nil
	})

	cases := []struct {
		name    string
		addr    string
		routeID string
		connect func(addr string, roots *x509.CertPool, id string) error
	}{
		{"direct/http1", directAddr, "direct:0", connectHTTP1},
		{"direct/http2", directAddr, "direct:0", connectHTTP2},
		{"dispatched/http1", dispatchedAddr, "dispatched:1", connectHTTP1},
		{"dispatched/http2", dispatchedAddr, "dispatched:1", connectHTTP2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.connect(tc.addr, roots, tc.name); err != nil {
				t.Fatalf("tunnel through %s: %v", connectSNI, err)
			}
			req := connectRequest(t, events, tc.name)
			if req == nil {
				t.Fatal("plugin saw no request")
			}
			if req.RouteID != tc.routeID || req.Domain != connectSNI || req.MatchDomain != "a" || req.Host != connectAuthority {
				t.Fatalf("plugin saw route %q domain %q group key %q host %q, want %q %q %q %q",
					req.RouteID, req.Domain, req.MatchDomain, req.Host,
					tc.routeID, connectSNI, "a", connectAuthority)
			}
		})
	}

	// Stop prox only after every tunnel's disconnect reached the plugin.
	waitFor(t, "plugin disconnects", func() bool {
		requests, disconnects := 0, 0
		for _, ev := range probeEvents(t, events) {
			if ev.Request != nil {
				requests++
			}
			if ev.Disconnect != nil {
				disconnects++
			}
		}
		return disconnects == requests
	})
}

// connectRequest returns the on_request payload the probe recorded for id.
func connectRequest(t *testing.T, events, id string) *plugin.RequestInfo {
	t.Helper()
	for _, ev := range probeEvents(t, events) {
		if ev.Request != nil && ev.Request.Headers["X-Case"] == id {
			return ev.Request
		}
	}
	return nil
}

func connectTLSConfig(roots *x509.CertPool, proto string) *tls.Config {
	return &tls.Config{ServerName: connectSNI, RootCAs: roots, NextProtos: []string{proto}}
}

// connectHTTP1 opens a CONNECT tunnel over HTTP/1.1 and echoes a few bytes through it.
func connectHTTP1(addr string, roots *x509.CertPool, id string) error {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", addr, connectTLSConfig(roots, "http/1.1"))
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nX-Case: %s\r\n\r\n", connectAuthority, connectAuthority, id); err != nil {
		return err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return echoPing(conn, br)
}

// connectHTTP2 opens a CONNECT stream over HTTP/2 and echoes a few bytes through it.
func connectHTTP2(addr string, roots *x509.CertPool, id string) error {
	h2Only := new(http.Protocols)
	h2Only.SetHTTP2(true)
	tr := &http.Transport{
		Protocols: h2Only,
		DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := tls.Dialer{Config: connectTLSConfig(roots, "h2")}
			return d.DialContext(ctx, "tcp", addr)
		},
	}
	defer tr.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pr, pw := io.Pipe()
	defer pw.Close()
	req := (&http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Scheme: "https", Host: connectAuthority},
		Host:   connectAuthority,
		Header: http.Header{"X-Case": {id}},
		Body:   pr,
	}).WithContext(ctx)

	// Response headers arrive once the first bytes flow through the tunnel.
	go func() { _, _ = io.WriteString(pw, "ping") }()
	resp, err := tr.RoundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return readPing(resp.Body)
}

func echoPing(w io.Writer, r io.Reader) error {
	if _, err := io.WriteString(w, "ping"); err != nil {
		return err
	}
	return readPing(r)
}

func readPing(r io.Reader) error {
	buf := make([]byte, 4)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	if string(buf) != "ping" {
		return fmt.Errorf("echo %q, want %q", buf, "ping")
	}
	return nil
}
