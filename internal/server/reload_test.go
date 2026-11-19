package server

import (
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
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dortanes/prox/internal/config"
)

// writeCert writes a self-signed pair for name as <base>.crt and <base>.key.
func writeCert(t *testing.T, dir, base, name string, serial int64) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, base+".crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeFile(t, filepath.Join(dir, base+".key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func tlsConfig(addr, certDir string, strict bool, routes ...*config.Route) *config.Config {
	return &config.Config{
		Services: map[string]*config.Service{"web": {
			Listen: addr, TLS: true, TLSCert: certDir, StrictSNI: strict, Routes: routes,
		}},
		Actions: map[string]*config.Action{
			"static": {Type: config.ActionTypeStatic, Status: 200},
			"drop":   {Type: config.ActionTypeDrop},
		},
		Plugins:   map[string]*config.Plugin{},
		Resources: map[string]*config.Resource{},
	}
}

func catchAll() *config.Route {
	return &config.Route{Action: config.ActionRef{Name: "static"}}
}

func domainRoute(domain string, action config.ActionRef) *config.Route {
	return &config.Route{Match: &config.Match{Domain: domain}, Action: action}
}

func startGroup(t *testing.T, cfg *config.Config) *Group {
	t.Helper()
	g, err := Build(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.ListenAndServe(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	addr := cfg.Services["web"].Listen
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return g
		}
		if time.Now().After(deadline) {
			t.Fatalf("listener not ready: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func handshake(addr, name string) (*tls.Conn, error) {
	return tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr,
		&tls.Config{ServerName: name, InsecureSkipVerify: true}) //nolint:gosec // tests inspect the presented certificate
}

func servedSerial(t *testing.T, addr, name string) int64 {
	t.Helper()
	c, err := handshake(addr, name)
	if err != nil {
		t.Fatalf("handshake %s: %v", name, err)
	}
	defer c.Close()
	return c.ConnectionState().PeerCertificates[0].SerialNumber.Int64()
}

// handshakeRejected reports whether the server refuses the name. A TLS 1.3
// client learns of the refusal on its first read.
func handshakeRejected(addr, name string) bool {
	c, err := handshake(addr, name)
	if err != nil {
		return true
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, err = c.Read(make([]byte, 1))
	return err != nil && !errors.Is(err, os.ErrDeadlineExceeded)
}

func TestReload_SwapsCertificateAndKeepsConnections(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, dir, "site", "a.example.com", 1)
	addr := freeAddr(t)
	cfg := tlsConfig(addr, dir, false, catchAll())
	g := startGroup(t, cfg)

	open, err := handshake(addr, "a.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()

	writeCert(t, dir, "site", "a.example.com", 2)
	if _, err := g.Reload(cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := servedSerial(t, addr, "a.example.com"); got != 2 {
		t.Fatalf("serial after reload = %d, want 2", got)
	}

	if _, err := fmt.Fprint(open, "GET / HTTP/1.1\r\nHost: a.example.com\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatalf("write on existing connection: %v", err)
	}
	if body, err := io.ReadAll(open); err != nil || len(body) == 0 {
		t.Fatalf("existing connection broke: %d bytes, %v", len(body), err)
	}
}

func TestReload_InvalidPairKeepsPreviousCertificates(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, dir, "site", "a.example.com", 1)
	addr := freeAddr(t)
	cfg := tlsConfig(addr, dir, false, catchAll())
	g := startGroup(t, cfg)

	writeFile(t, filepath.Join(dir, "site.key"), []byte("not a key"))
	next := tlsConfig(addr, dir, false, domainRoute("b.example.com", config.ActionRef{Name: "drop"}), catchAll())
	if _, err := g.Reload(next); err == nil {
		t.Fatal("Reload accepted an invalid pair")
	}
	if got := servedSerial(t, addr, "a.example.com"); got != 1 {
		t.Fatalf("serial = %d, want 1", got)
	}
	if g.CurrentConfig() != cfg {
		t.Fatal("failed reload replaced the active config")
	}
}

func TestEmptyCertDirectory_StartsAndLoadsLater(t *testing.T) {
	for name, dir := range map[string]string{
		"empty":   t.TempDir(),
		"missing": filepath.Join(t.TempDir(), "certs"),
	} {
		t.Run(name, func(t *testing.T) {
			addr := freeAddr(t)
			cfg := tlsConfig(addr, dir, false, catchAll())
			g := startGroup(t, cfg)
			if !handshakeRejected(addr, "a.example.com") {
				t.Fatal("handshake succeeded without certificates")
			}

			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			writeCert(t, dir, "site", "a.example.com", 7)
			if _, err := g.Reload(cfg); err != nil {
				t.Fatalf("Reload: %v", err)
			}
			if got := servedSerial(t, addr, "a.example.com"); got != 7 {
				t.Fatalf("serial = %d, want 7", got)
			}
		})
	}
}

func TestStrictSNI(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, dir, "site", "a.example.com", 1)
	writeCert(t, dir, "wild", "*.wild.example.com", 2)

	t.Run("off serves the first certificate", func(t *testing.T) {
		addr := freeAddr(t)
		startGroup(t, tlsConfig(addr, dir, false, catchAll()))
		if got := servedSerial(t, addr, "other.example.org"); got != 1 {
			t.Fatalf("serial = %d, want 1", got)
		}
	})

	t.Run("on rejects unmatched names", func(t *testing.T) {
		addr := freeAddr(t)
		startGroup(t, tlsConfig(addr, dir, true, catchAll()))
		if !handshakeRejected(addr, "other.example.org") {
			t.Fatal("unmatched name was served")
		}
		if got := servedSerial(t, addr, "x.wild.example.com"); got != 2 {
			t.Fatalf("wildcard serial = %d, want 2", got)
		}
	})
}

func TestDropRoute_ClosesDuringHandshake(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, dir, "site", "a.example.com", 1)
	addr := freeAddr(t)
	startGroup(t, tlsConfig(addr, dir, false,
		domainRoute("a.example.com", config.ActionRef{Name: "drop"}), catchAll()))

	start := time.Now()
	if !handshakeRejected(addr, "a.example.com") {
		t.Fatal("dropped name completed a handshake")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("drop took %s", elapsed)
	}
}

func TestReload_PassRoutesComeAndGo(t *testing.T) {
	upstream, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		for {
			c, err := upstream.Accept()
			if err != nil {
				return
			}
			// Answers the ClientHello with bytes no TLS client accepts.
			_, _ = c.Write([]byte("upstream"))
			c.Close()
		}
	}()

	dir := t.TempDir()
	writeCert(t, dir, "site", "a.example.com", 1)
	addr := freeAddr(t)
	cfg := tlsConfig(addr, dir, false, catchAll())
	g := startGroup(t, cfg)

	withPass := tlsConfig(addr, dir, false,
		domainRoute("a.example.com", config.ActionRef{Inline: &config.Action{Type: config.ActionTypePass, Upstream: upstream.Addr().String()}}),
		catchAll())
	if _, err := g.Reload(withPass); err != nil {
		t.Fatal(err)
	}
	if _, err := handshake(addr, "a.example.com"); err == nil {
		t.Fatal("handshake succeeded, want the raw upstream")
	}

	if _, err := g.Reload(cfg); err != nil {
		t.Fatal(err)
	}
	if got := servedSerial(t, addr, "a.example.com"); got != 1 {
		t.Fatalf("serial = %d, want 1", got)
	}
}

func TestReload_ReportsRestartRequired(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, dir, "site", "a.example.com", 1)
	addr := freeAddr(t)
	g := startGroup(t, tlsConfig(addr, dir, false, catchAll()))

	next := tlsConfig(addr, dir, false, catchAll())
	next.Services["web"].Config = &config.ServerConfig{MaxConnections: 5}
	for range 2 { // stays listed until a restart
		report, err := g.Reload(next)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(report.RestartRequired, []string{`service "web": config.max_connections`}) {
			t.Fatalf("restart required = %v", report.RestartRequired)
		}
	}
}

func TestCertificateStatus_ListsManualCertificates(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, dir, "site", "a.example.com", 255)
	g := startGroup(t, tlsConfig(freeAddr(t), dir, false, catchAll()))

	status := g.CertificateStatus()
	if len(status) != 1 {
		t.Fatalf("got %d entries", len(status))
	}
	s := status[0]
	if s.Domain != "a.example.com" || s.Serial != "ff" || s.Status != "active" ||
		s.Source != filepath.Join(dir, "site.crt") || s.Expires == nil {
		t.Fatalf("unexpected status: %+v", s)
	}
}

func TestReload_AddsAndRemovesPlugin(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	script := filepath.Join(dir, "plugin.sh")
	writeFile(t, script, []byte("#!/bin/sh\necho \"$1\" > \""+marker+"\"\nexec cat >/dev/null\n"))
	if err := os.Chmod(script, 0o700); err != nil {
		t.Fatal(err)
	}

	addr := freeAddr(t)
	certs := t.TempDir()
	writeCert(t, certs, "site", "a.example.com", 1)
	cfg := tlsConfig(addr, certs, false, catchAll())
	g := startGroup(t, cfg)
	h := g.handlers["web"]

	next := tlsConfig(addr, certs, false, catchAll())
	next.Plugins["p"] = &config.Plugin{Path: script, Args: []string{"from-args"}, Autostart: true}
	if _, err := g.Reload(next); err != nil {
		t.Fatal(err)
	}
	if h.plugins.Load() == nil {
		t.Fatal("handler did not receive the new plugin manager")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(marker)
		if string(data) == "from-args\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin did not start with its args: %q", data)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := g.Reload(cfg); err != nil {
		t.Fatal(err)
	}
	if h.plugins.Load() != nil {
		t.Fatal("handler kept the removed plugin manager")
	}
}
