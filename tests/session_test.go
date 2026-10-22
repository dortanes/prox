package tests

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dortanes/prox/internal/plugin"
	"golang.org/x/net/http2"
)

const usageInterval = 100 * time.Millisecond

// byteCount is the traffic of one client as prox counts it.
type byteCount struct {
	rx int64 // client → prox
	tx int64 // prox → client
}

// TestPluginSessions covers session keys, usage reports and session drops
// for every long-lived connection type prox relays.
func TestPluginSessions(t *testing.T) {
	env := startSessionEnv(t)

	cases := []struct {
		name    string
		routeID string
		open    func(t *testing.T, session string) sessionClient
		l7      bool
	}{
		{"websocket", "web:0", env.openWebSocket, true},
		{"upgrade", "web:1", env.openUpgrade, true},
		{"connect", "web:3", env.openConnect, true},
		{"h2-stream", "secure:1", env.openH2Stream, true},
		{"h2-connect", "secure:2", env.openH2Connect, true},
		{"l4-pass", "secure:0", env.openL4, false},
	}

	for _, tc := range cases {
		t.Run("usage/"+tc.name, func(t *testing.T) {
			session := "usage-" + tc.name
			c := tc.open(t, session)

			// Each exchange must show up while the connection is still open.
			for _, n := range []int{1000, 500} {
				c.exchange(t, n)
				env.waitUsage(t, tc.routeID, session, c.counts())
			}

			c.closeGracefully(t)
			final := c.counts()
			env.waitUsage(t, tc.routeID, session, final)
			time.Sleep(3 * usageInterval)
			if got := env.usage(t, tc.routeID, session); got != final {
				t.Fatalf("usage after close = %+v, want %+v", got, final)
			}

			if !tc.l7 {
				return
			}
			d := env.waitDisconnect(t, session)
			if d.RouteID != tc.routeID || d.BytesRx != final.rx || d.BytesTx != final.tx {
				t.Fatalf("disconnect = %+v, want route %s and %+v", d, tc.routeID, final)
			}
		})
	}

	t.Run("drop", func(t *testing.T) {
		dropped := make([]sessionClient, len(cases))
		kept := make([]sessionClient, len(cases))
		var keys []string
		for i, tc := range cases {
			dropped[i] = tc.open(t, "drop-"+tc.name)
			dropped[i].exchange(t, 100)
			kept[i] = tc.open(t, "keep-"+tc.name)
			kept[i].exchange(t, 100)
			keys = append(keys, "drop-"+tc.name)
		}

		env.dropSessions(t, append(keys, "no-such-session")...)

		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dropped[i].expectClosed(t)
				kept[i].exchange(t, 100)
				kept[i].closeGracefully(t)
			})
		}
	})
}

// sessionClient is one client connection or stream through prox.
type sessionClient interface {
	// exchange sends n bytes and reads their echo.
	exchange(t *testing.T, n int)
	counts() byteCount
	closeGracefully(t *testing.T)
	expectClosed(t *testing.T)
}

type sessionEnv struct {
	events     string
	webAddr    string
	secureAddr string
	roots      *x509.CertPool
	h2         *http2.Transport
}

func startSessionEnv(t *testing.T) *sessionEnv {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	env := &sessionEnv{events: filepath.Join(dir, "events.jsonl")}
	t.Setenv(probeEventsEnv, env.events)

	cert, certFile, keyFile := writeTestCert(t, dir)
	env.roots = x509.NewCertPool()
	env.roots.AddCert(cert.Leaf)
	webPort, securePort := freePort(t), freePort(t)
	env.webAddr = fmt.Sprintf("127.0.0.1:%d", webPort)
	env.secureAddr = fmt.Sprintf("127.0.0.1:%d", securePort)

	cfg := fmt.Sprintf(`{
		plugins: { probe: { path: %q, usage_interval: %q } },
		services: {
			web: {
				listen: %q,
				plugins: ["probe"],
				routes: [
					{ match: { path: "/ws" }, action: "upgrade" },
					{ match: { path: "/upgrade" }, action: "upgrade" },
					{ match: { path: "/control" }, action: "upgrade" },
					{ action: "connect" },
				],
			},
			secure: {
				listen: %q,
				tls: true,
				tls_cert: %q,
				tls_key: %q,
				plugins: ["probe"],
				routes: [
					{ match: { domain: "*.l4.test" }, action: "l4" },
					{ match: { path: "/stream" }, action: "stream" },
					{ action: "connect" },
				],
			},
		},
		actions: {
			upgrade: { type: "proxy", upstream: %q },
			connect: { type: "proxy", upstream: %q },
			stream: { type: "proxy", upstream: %q, stream: true },
			l4: { type: "pass", upstream: %q },
		},
	}`,
		exe, usageInterval.String(),
		env.webAddr,
		env.secureAddr, certFile, keyFile,
		upgradeEchoUpstream(t), connectEchoUpstream(t), streamEchoUpstream(t), tlsEchoUpstream(t, cert),
	)
	stop := startProx(t, writeConfig(t, cfg), webPort)
	t.Cleanup(stop)
	waitForPort(t, securePort, 3*time.Second)

	env.h2 = &http2.Transport{
		DialTLSContext: func(_ context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
			return tls.Dial("tcp", env.secureAddr, &tls.Config{
				ServerName: "prox.test",
				RootCAs:    env.roots,
				NextProtos: []string{"h2"},
			})
		},
	}
	t.Cleanup(env.h2.CloseIdleConnections)

	waitFor(t, "probe plugin ready", func() bool { return env.control(t, "none") == http.StatusAccepted })
	return env
}

// control sends a request the probe answers with 202 after dropping the
// sessions in drop; before the probe is ready it reaches the upstream.
func (e *sessionEnv) control(t *testing.T, drop string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+e.webAddr+"/control", nil)
	req.Header.Set("X-Drop", drop)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("control request: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (e *sessionEnv) dropSessions(t *testing.T, sessions ...string) {
	t.Helper()
	if status := e.control(t, strings.Join(sessions, ",")); status != http.StatusAccepted {
		t.Fatalf("drop request status = %d", status)
	}
}

func (e *sessionEnv) usage(t *testing.T, routeID, session string) byteCount {
	t.Helper()
	var total byteCount
	for _, ev := range probeEvents(t, e.events) {
		if ev.Usage == nil {
			continue
		}
		for _, s := range ev.Usage.Sessions {
			if s.RouteID == routeID && s.Session == session {
				total.rx += s.BytesRx
				total.tx += s.BytesTx
			}
		}
	}
	return total
}

func (e *sessionEnv) waitUsage(t *testing.T, routeID, session string, want byteCount) {
	t.Helper()
	waitFor(t, fmt.Sprintf("usage %+v for %s on %s", want, session, routeID), func() bool {
		got := e.usage(t, routeID, session)
		if got.rx > want.rx || got.tx > want.tx {
			t.Fatalf("usage %+v exceeds the %+v the client moved", got, want)
		}
		return got == want
	})
}

func (e *sessionEnv) waitDisconnect(t *testing.T, session string) plugin.DisconnectInfo {
	t.Helper()
	var found *plugin.DisconnectInfo
	waitFor(t, "disconnect of "+session, func() bool {
		for _, ev := range probeEvents(t, e.events) {
			if ev.Disconnect != nil && ev.Disconnect.Session == session {
				found = ev.Disconnect
				return true
			}
		}
		return false
	})
	return *found
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// --- raw TCP clients: hijacked HTTP/1.1 connections and L4 pass ---

// countConn counts the bytes a client connection moves.
type countConn struct {
	net.Conn
	sent, received *atomic.Int64
}

func (c *countConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.received.Add(int64(n))
	return n, err
}

func (c *countConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.sent.Add(int64(n))
	return n, err
}

type rawClient struct {
	tcp            *net.TCPConn
	r              io.Reader
	w              io.Writer
	closeWrite     func() error
	sent, received atomic.Int64
}

func dialRaw(t *testing.T, addr string) (*rawClient, *countConn) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &rawClient{tcp: conn.(*net.TCPConn)}
	return c, &countConn{Conn: conn, sent: &c.sent, received: &c.received}
}

// dialHijacked sends an HTTP/1.1 request that switches the connection to a
// raw byte stream. Request header bytes are not session traffic; the
// response that switches protocols is.
func (e *sessionEnv) dialHijacked(t *testing.T, request string, wantStatus int) sessionClient {
	t.Helper()
	c, cc := dialRaw(t, e.webAddr)
	if _, err := io.WriteString(cc, request); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(cc)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", resp.StatusCode, wantStatus)
	}
	c.sent.Store(0)
	c.r, c.w = br, cc
	c.closeWrite = c.tcp.CloseWrite
	return c
}

func (e *sessionEnv) openWebSocket(t *testing.T, session string) sessionClient {
	return e.dialHijacked(t, "GET /ws HTTP/1.1\r\nHost: prox.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n"+
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nX-Session: "+session+"\r\n\r\n",
		http.StatusSwitchingProtocols)
}

func (e *sessionEnv) openUpgrade(t *testing.T, session string) sessionClient {
	return e.dialHijacked(t, "GET /upgrade HTTP/1.1\r\nHost: prox.test\r\nConnection: Upgrade\r\nUpgrade: probe\r\n"+
		"X-Session: "+session+"\r\n\r\n", http.StatusSwitchingProtocols)
}

func (e *sessionEnv) openConnect(t *testing.T, session string) sessionClient {
	return e.dialHijacked(t, "CONNECT target.test:443 HTTP/1.1\r\nHost: target.test:443\r\nX-Session: "+session+"\r\n\r\n",
		http.StatusOK)
}

// openL4 opens a TLS connection relayed by a pass route; the probe takes the
// session from the first SNI label. Every TCP byte counts.
func (e *sessionEnv) openL4(t *testing.T, session string) sessionClient {
	t.Helper()
	c, cc := dialRaw(t, e.secureAddr)
	conn := tls.Client(cc, &tls.Config{ServerName: session + ".l4.test", RootCAs: e.roots})
	if err := conn.Handshake(); err != nil {
		t.Fatalf("tls handshake: %v", err)
	}
	c.r, c.w = conn, conn
	c.closeWrite = conn.CloseWrite
	return c
}

func (c *rawClient) exchange(t *testing.T, n int) {
	t.Helper()
	payload := bytes.Repeat([]byte("x"), n)
	if _, err := c.w.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	echo := make([]byte, n)
	_ = c.tcp.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c.r, echo); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(echo, payload) {
		t.Fatal("echo mismatch")
	}
}

func (c *rawClient) counts() byteCount {
	return byteCount{rx: c.sent.Load(), tx: c.received.Load()}
}

func (c *rawClient) closeGracefully(t *testing.T) {
	t.Helper()
	if err := c.closeWrite(); err != nil {
		t.Fatalf("close write: %v", err)
	}
	_ = c.tcp.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.Copy(io.Discard, c.r); err != nil {
		t.Fatalf("drain: %v", err)
	}
	c.tcp.Close()
}

func (c *rawClient) expectClosed(t *testing.T) {
	t.Helper()
	_ = c.tcp.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := c.r.Read(make([]byte, 1))
	var ne net.Error
	if err == nil || (errors.As(err, &ne) && ne.Timeout()) {
		t.Fatalf("connection still open after drop (err = %v)", err)
	}
}

// --- HTTP/2 streams ---

type h2Client struct {
	pw       *io.PipeWriter
	result   chan h2Result
	body     io.ReadCloser
	sent     int64
	received int64
}

type h2Result struct {
	resp *http.Response
	err  error
}

func (e *sessionEnv) startH2(t *testing.T, req *http.Request, pw *io.PipeWriter) sessionClient {
	t.Helper()
	c := &h2Client{pw: pw, result: make(chan h2Result, 1)}
	go func() {
		resp, err := e.h2.RoundTrip(req)
		c.result <- h2Result{resp, err}
	}()
	t.Cleanup(func() {
		pw.Close()
		if c.body != nil {
			c.body.Close()
		}
	})
	return c
}

func (e *sessionEnv) openH2Stream(t *testing.T, session string) sessionClient {
	pr, pw := io.Pipe()
	req, _ := http.NewRequest(http.MethodPost, "https://prox.test/stream", pr)
	req.Header.Set("X-Session", session)
	return e.startH2(t, req, pw)
}

func (e *sessionEnv) openH2Connect(t *testing.T, session string) sessionClient {
	pr, pw := io.Pipe()
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Scheme: "https", Host: "target.test:443"},
		Host:   "target.test:443",
		Header: http.Header{"X-Session": {session}},
		Body:   pr,
	}
	return e.startH2(t, req, pw)
}

// response waits for the response headers, which arrive once the first bytes flow.
func (c *h2Client) response(t *testing.T) io.Reader {
	t.Helper()
	if c.body == nil {
		select {
		case res := <-c.result:
			if res.err != nil {
				t.Fatalf("h2 round trip: %v", res.err)
			}
			if res.resp.StatusCode != http.StatusOK {
				t.Fatalf("h2 status = %d", res.resp.StatusCode)
			}
			c.body = res.resp.Body
		case <-time.After(5 * time.Second):
			t.Fatal("h2 response headers timed out")
		}
	}
	return c.body
}

func (c *h2Client) exchange(t *testing.T, n int) {
	t.Helper()
	payload := bytes.Repeat([]byte("x"), n)
	if _, err := c.pw.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	c.sent += int64(n)
	echo := make([]byte, n)
	if _, err := io.ReadFull(c.response(t), echo); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	c.received += int64(n)
	if !bytes.Equal(echo, payload) {
		t.Fatal("echo mismatch")
	}
}

func (c *h2Client) counts() byteCount {
	return byteCount{rx: c.sent, tx: c.received}
}

func (c *h2Client) closeGracefully(t *testing.T) {
	t.Helper()
	c.pw.Close()
	n, err := io.Copy(io.Discard, c.response(t))
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	c.received += n
	c.body.Close()
}

func (c *h2Client) expectClosed(t *testing.T) {
	t.Helper()
	body := c.response(t)
	done := make(chan error, 1)
	go func() {
		_, err := body.Read(make([]byte, 1))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stream delivered data after drop")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream still open after drop")
	}
}

// --- upstreams ---

// upgradeEchoUpstream switches any Upgrade request to an echo stream.
func upgradeEchoUpstream(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto := r.Header.Get("Upgrade")
		if proto == "" {
			w.WriteHeader(http.StatusOK)
			return
		}
		conn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", proto)
		_, _ = io.Copy(conn, brw)
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// connectEchoUpstream accepts a CONNECT and echoes the tunnel.
func connectEchoUpstream(t *testing.T) string {
	return serveTCP(t, nil, func(c net.Conn) {
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil || req.Method != http.MethodConnect {
			return
		}
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n\r\n")
		_, _ = io.Copy(c, br)
	})
}

// streamEchoUpstream echoes a request body into the response as it arrives.
func streamEchoUpstream(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		_ = rc.EnableFullDuplex()
		w.WriteHeader(http.StatusOK)
		_ = rc.Flush()
		buf := make([]byte, 32<<10)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				_ = rc.Flush()
			}
			if err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// tlsEchoUpstream terminates TLS and echoes.
func tlsEchoUpstream(t *testing.T, cert tls.Certificate) string {
	return serveTCP(t, &tls.Config{Certificates: []tls.Certificate{cert}}, func(c net.Conn) {
		_, _ = io.Copy(c, c)
	})
}

func serveTCP(t *testing.T, tlsCfg *tls.Config, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if tlsCfg != nil {
		ln = tls.NewListener(ln, tlsCfg)
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

// writeTestCert creates a self-signed certificate for *.test and writes it as
// PEM files into dir.
func writeTestCert(t *testing.T, dir string) (tls.Certificate, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "prox.test"},
		DNSNames:              []string{"prox.test", "*.l4.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert, certFile, keyFile
}
