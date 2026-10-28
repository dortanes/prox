package tests

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"testing"
	"time"
)

// TestTLS_H2Setting checks the protocol a TLS listener negotiates with a
// client that offers both HTTP/2 and HTTP/1.1.
func TestTLS_H2Setting(t *testing.T) {
	dir := t.TempDir()
	cert, certFile, keyFile := writeTestCert(t, dir)
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)
	defaultPort, disabledPort := freePort(t), freePort(t)

	cfg := fmt.Sprintf(`{
		services: {
			h2default: {
				listen: "127.0.0.1:%d",
				tls: true,
				tls_cert: %q,
				tls_key: %q,
				routes: [{ action: "a" }],
			},
			h2disabled: {
				listen: "127.0.0.1:%d",
				tls: true,
				tls_cert: %q,
				tls_key: %q,
				h2: false,
				routes: [{ action: "a" }],
			},
		},
		actions: {
			a: { type: "proxy", upstream: "127.0.0.1:1" },
		},
	}`,
		defaultPort, certFile, keyFile,
		disabledPort, certFile, keyFile,
	)
	t.Cleanup(startProx(t, writeConfig(t, cfg), defaultPort))
	waitForPort(t, disabledPort, 3*time.Second)

	for _, tc := range []struct {
		name string
		port int
		want string
	}{
		{"default", defaultPort, "h2"},
		{"h2 false", disabledPort, "http/1.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp",
				fmt.Sprintf("127.0.0.1:%d", tc.port),
				&tls.Config{ServerName: "prox.test", RootCAs: roots, NextProtos: []string{"h2", "http/1.1"}})
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer conn.Close()
			if got := conn.ConnectionState().NegotiatedProtocol; got != tc.want {
				t.Fatalf("negotiated %q, want %q", got, tc.want)
			}
		})
	}
}
