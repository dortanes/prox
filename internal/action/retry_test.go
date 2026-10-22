package action

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dortanes/prox/internal/config"
	"github.com/dortanes/prox/internal/router"
)

// refusedAddr returns a local address that refuses connections.
func refusedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// balancedHandler matches every request to one round-robin route over
// targets and serves it with a proxy built from act.
func balancedHandler(t *testing.T, targets []string, act *config.Action, fallback http.Handler) http.Handler {
	t.Helper()
	rt := router.New([]*config.Route{{
		Balancer: &config.BalancerConfig{Type: config.BalancerRoundRobin, Targets: targets},
		Action:   config.ActionRef{Name: "proxy"},
	}})
	act.Type = config.ActionTypeProxy
	p, err := NewProxy(act, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fallback != nil {
		p.SetFallback(fallback)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, _ = rt.Match(r)
		p.ServeHTTP(w, r)
	})
}

// serveRaw accepts TCP connections and hands each to handle.
func serveRaw(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				handle(c)
			}()
		}
	}()
	return ln.Addr().String()
}

func TestRetry_HTTPRequestReachesNextTarget(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s %s", r.Method, body)
	}))
	defer live.Close()
	liveAddr := live.Listener.Addr().String()

	for _, tc := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPost, "payload"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			h := balancedHandler(t, []string{refusedAddr(t), liveAddr}, &config.Action{Upstream: "http://{target}"}, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body)))

			want := tc.method + " " + tc.body
			if rec.Code != http.StatusOK || rec.Body.String() != want {
				t.Fatalf("got %d %q, want 200 %q", rec.Code, rec.Body.String(), want)
			}
		})
	}
}

func TestRetry_RelaysReachNextTarget(t *testing.T) {
	upgradeEcho := func(c net.Conn) {
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", req.Header.Get("Upgrade"))
		_, _ = io.Copy(c, br)
	}
	connectEcho := func(c net.Conn) {
		br := bufio.NewReader(c)
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n\r\n")
		_, _ = io.Copy(c, br)
	}
	answerOK := func(c net.Conn) {
		if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
			return
		}
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
	}

	cases := []struct {
		name     string
		act      config.Action
		upstream func(net.Conn)
		request  string
		status   string
		echo     bool
	}{
		{
			name:     "websocket",
			act:      config.Action{Upstream: "http://{target}"},
			upstream: upgradeEcho,
			request:  "GET /ws HTTP/1.1\r\nHost: prox.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n",
			status:   "HTTP/1.1 101",
			echo:     true,
		},
		{
			name:     "connect",
			act:      config.Action{Upstream: "http://{target}"},
			upstream: connectEcho,
			request:  "CONNECT target.test:443 HTTP/1.1\r\nHost: target.test:443\r\n\r\n",
			status:   "HTTP/1.1 200",
			echo:     true,
		},
		{
			name:     "stream",
			act:      config.Action{Upstream: "http://{target}", Stream: true},
			upstream: answerOK,
			request:  "GET /stream HTTP/1.1\r\nHost: prox.test\r\n\r\n",
			status:   "HTTP/1.1 200",
		},
		{
			name:     "forward proxy",
			act:      config.Action{Upstream: "http://{target}"},
			upstream: answerOK,
			request:  "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n",
			status:   "HTTP/1.1 200",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			targets := []string{refusedAddr(t), serveRaw(t, tc.upstream)}
			prox := httptest.NewServer(balancedHandler(t, targets, &tc.act, nil))
			defer prox.Close()

			conn, err := net.Dial("tcp", prox.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.WriteString(conn, tc.request); err != nil {
				t.Fatal(err)
			}
			br := bufio.NewReader(conn)
			line, err := br.ReadString('\n')
			if err != nil || !strings.HasPrefix(line, tc.status) {
				t.Fatalf("status line %q (err %v), want %s", line, err, tc.status)
			}
			if !tc.echo {
				return
			}
			for line != "\r\n" {
				if line, err = br.ReadString('\n'); err != nil {
					t.Fatal(err)
				}
			}
			_, _ = io.WriteString(conn, "ping")
			got := make([]byte, 4)
			if _, err := io.ReadFull(br, got); err != nil || string(got) != "ping" {
				t.Fatalf("echo %q (err %v), want ping", got, err)
			}
		})
	}
}

func TestRetry_NotAfterRequestSent(t *testing.T) {
	var liveHits atomic.Int32
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		liveHits.Add(1)
	}))
	defer live.Close()

	// Reads the whole request, then drops the connection without a response.
	hangUp := serveRaw(t, func(c net.Conn) {
		req, err := http.ReadRequest(bufio.NewReader(c))
		if err == nil {
			_, _ = io.Copy(io.Discard, req.Body)
		}
	})

	for _, tc := range []struct {
		method string
		body   io.Reader
	}{
		{http.MethodGet, nil},
		{http.MethodPost, strings.NewReader("payload")},
	} {
		t.Run(tc.method, func(t *testing.T) {
			h := balancedHandler(t, []string{hangUp, live.Listener.Addr().String()}, &config.Action{Upstream: "http://{target}"}, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, "/", tc.body))

			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", rec.Code)
			}
		})
	}
	if n := liveHits.Load(); n != 0 {
		t.Fatalf("second target got %d requests, want none after the first one was sent", n)
	}
}

func TestRetry_AllTargetsDownFallsBack(t *testing.T) {
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "fallback")
	})
	h := balancedHandler(t, []string{refusedAddr(t), refusedAddr(t)}, &config.Action{Upstream: "http://{target}"}, fallback)

	// The first request ejects both targets; the second finds the group empty.
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "fallback" {
			t.Fatalf("request %d: got %d %q, want the fallback", i, rec.Code, rec.Body.String())
		}
	}
}
