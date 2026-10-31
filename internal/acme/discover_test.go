package acme

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dortanes/prox/internal/config"
)

func TestDiscoverDomains_StalledProviderTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	// Close waits for active requests, so the stalled one must be cut first.
	defer srv.CloseClientConnections()

	api, timeout := cloudflareAPI, discoveryTimeout
	cloudflareAPI, discoveryTimeout = srv.URL, 50*time.Millisecond
	defer func() { cloudflareAPI, discoveryTimeout = api, timeout }()

	done := make(chan error, 1)
	go func() {
		_, err := DiscoverDomains(context.Background(), &config.ACMEDNSConfig{Provider: "cloudflare", Token: "test"})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error from a stalled provider")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("discovery did not time out")
	}
}
