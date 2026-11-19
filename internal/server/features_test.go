package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dortanes/prox/internal/config"
)

func staticHandler(t *testing.T, cfg *config.Config) *swappableHandler {
	t.Helper()
	g, err := Build(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return g.handlers["web"]
}

func plainConfig(routes ...*config.Route) *config.Config {
	cfg := tlsConfig("127.0.0.1:0", "", false, routes...)
	cfg.Services["web"].TLS = false
	return cfg
}

func TestAccess_L7(t *testing.T) {
	route := catchAll()
	route.Access = &config.AccessConfig{Allow: []string{"192.0.2.0/24"}}
	h := staticHandler(t, plainConfig(route))

	for remote, want := range map[string]int{
		"192.0.2.9:1000":    http.StatusOK,
		"198.51.100.1:1000": http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d", remote, rec.Code, want)
		}
	}
}

func TestMetrics_CountsRoutesAndUpstreams(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusBadGateway)
		}
		_, _ = w.Write([]byte("hello"))
	}))
	defer upstream.Close()

	cfg := plainConfig(&config.Route{ID: "api", Action: config.ActionRef{Name: "app"}})
	cfg.Actions["app"] = &config.Action{Type: config.ActionTypeProxy, Upstream: upstream.URL}
	cfg.Admin = &config.AdminConfig{Listen: "127.0.0.1:0"}
	g, err := Build(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/", "/fail"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("abc"))
		g.handlers["web"].ServeHTTP(httptest.NewRecorder(), req)
	}

	snap := g.Metrics()
	if len(snap.Routes) != 1 || len(snap.Upstreams) != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	for _, e := range []struct {
		id    string
		entry int64
		errs  int64
		in    int64
		out   int64
		act   int64
	}{
		{"web:api", snap.Routes[0].Requests, snap.Routes[0].Errors, snap.Routes[0].BytesIn, snap.Routes[0].BytesOut, snap.Routes[0].Active},
		{upstream.URL, snap.Upstreams[0].Requests, snap.Upstreams[0].Errors, snap.Upstreams[0].BytesIn, snap.Upstreams[0].BytesOut, snap.Upstreams[0].Active},
	} {
		if e.entry != 2 || e.errs != 1 || e.in != 6 || e.out != 10 || e.act != 0 {
			t.Errorf("%s: %+v", e.id, e)
		}
	}
	if snap.Routes[0].ID != "web:api" {
		t.Errorf("route id = %q", snap.Routes[0].ID)
	}
}

func TestStableRouteID_SurvivesReorder(t *testing.T) {
	route := &config.Route{ID: "api", Plugins: []string{"p"}, Action: config.ActionRef{Name: "static"}}
	cfg := plainConfig(catchAll(), route)
	cfg.Plugins["p"] = &config.Plugin{Path: "/bin/true"}

	found := false
	for _, b := range buildPluginBindings(cfg, nil) {
		found = found || b.RouteID == "web:api"
	}
	if !found {
		t.Fatal("binding does not use the explicit id")
	}
}

// startPassUpstream records the first line each connection sends.
func startPassUpstream(t *testing.T) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	lines := make(chan string, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			lines <- line
			c.Close()
		}
	}()
	return ln.Addr().String(), lines
}

func passConfig(addr, certDir string, action *config.Action, access *config.AccessConfig) *config.Config {
	route := domainRoute("a.example.com", config.ActionRef{Name: "pass"})
	route.Access = access
	cfg := tlsConfig(addr, certDir, false, route, catchAll())
	cfg.Actions["pass"] = action
	return cfg
}

func TestPass_SendsProxyProtocol(t *testing.T) {
	upstream, lines := startPassUpstream(t)
	dir := t.TempDir()
	writeCert(t, dir, "site", "a.example.com", 1)
	addr := freeAddr(t)
	startGroup(t, passConfig(addr, dir, &config.Action{Type: config.ActionTypePass, Upstream: upstream, ProxyProtocol: "v1"}, nil))

	_, _ = handshake(addr, "a.example.com")
	select {
	case line := <-lines:
		if !strings.HasPrefix(line, "PROXY TCP4 127.0.0.1 127.0.0.1 ") {
			t.Fatalf("first line = %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upstream received nothing")
	}
}

func TestPass_AccessDenied(t *testing.T) {
	upstream, lines := startPassUpstream(t)
	dir := t.TempDir()
	writeCert(t, dir, "site", "a.example.com", 1)
	addr := freeAddr(t)
	startGroup(t, passConfig(addr, dir,
		&config.Action{Type: config.ActionTypePass, Upstream: upstream, ProxyProtocol: "v1"},
		&config.AccessConfig{Deny: []string{"127.0.0.1"}}))

	if !handshakeRejected(addr, "a.example.com") {
		t.Fatal("denied client completed a handshake")
	}
	select {
	case line := <-lines:
		t.Fatalf("upstream received %q", line)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestReload_RestartsPluginWhenArgsChange(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "args")
	script := filepath.Join(dir, "plugin.sh")
	writeFile(t, script, []byte("#!/bin/sh\necho \"$1\" > \""+marker+"\"\nexec cat >/dev/null\n"))
	if err := os.Chmod(script, 0o700); err != nil {
		t.Fatal(err)
	}
	certs := t.TempDir()
	writeCert(t, certs, "site", "a.example.com", 1)
	addr := freeAddr(t)

	withArg := func(arg string) *config.Config {
		cfg := tlsConfig(addr, certs, false, catchAll())
		cfg.Plugins["p"] = &config.Plugin{Path: script, Args: []string{arg}, Autostart: true}
		return cfg
	}
	waitFor := func(want string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			data, _ := os.ReadFile(marker)
			if string(data) == fmt.Sprintln(want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("marker = %q, want %q", data, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	g := startGroup(t, withArg("one"))
	waitFor("one")
	if _, err := g.Reload(withArg("two")); err != nil {
		t.Fatal(err)
	}
	waitFor("two")
}
