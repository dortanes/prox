package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labostack/prox/internal/config"
)

// testDeps returns a Deps with sensible defaults for testing.
func testDeps() Deps {
	cfg := &config.Config{
		Services: map[string]*config.Service{
			"web": {
				Listen: ":443",
				TLS:    true,
				ACME:   &config.ACMEConfig{Email: "test@example.com"},
				Routes: []*config.Route{
					{
						Match:  &config.Match{Domain: "example.com", Path: "/api/*"},
						Action: config.ActionRef{Name: "proxy"},
					},
					{
						Match:  &config.Match{Domain: "example.com"},
						Action: config.ActionRef{Name: "static"},
					},
				},
			},
			"internal": {
				Listen: ":8080",
				Routes: []*config.Route{
					{Action: config.ActionRef{Name: "proxy"}},
				},
			},
		},
		Actions: map[string]*config.Action{
			"proxy":  {Type: config.ActionTypeProxy, Upstream: "http://localhost:3000"},
			"static": {Type: config.ActionTypeStatic, Status: 200},
		},
		Plugins:   map[string]*config.Plugin{},
		Resources: map[string]*config.Resource{},
		Admin: &config.AdminConfig{
			Listen: "127.0.0.1:0",
			Token:  "test-secret",
		},
	}

	return Deps{
		StartTime: time.Now().Add(-2 * time.Hour),
		Version:   "1.0.0-test",
		GetConfig: func() *config.Config { return cfg },
		Reload: func() *ReloadResult {
			return &ReloadResult{OK: true, Routes: 3, Services: 2}
		},
		RouteCount:   func() int { return 3 },
		ServiceInfo:  func() []ServiceEntry { return nil },
		CertStatus:   func() []CertEntry { return nil },
		PluginInfo:   func() []PluginEntry { return nil },
		BalancerInfo: func() []BalancerEntry { return nil },
	}
}

func newTestServer(t *testing.T, deps Deps) *httptest.Server {
	t.Helper()
	cfg := &config.AdminConfig{Listen: "127.0.0.1:0"}
	if deps.GetConfig != nil {
		if c := deps.GetConfig(); c != nil && c.Admin != nil {
			cfg = c.Admin
		}
	}
	s := New(cfg, deps)
	return httptest.NewServer(s.httpServer.Handler)
}

func TestHealthEndpoint(t *testing.T) {
	deps := testDeps()
	ts := newTestServer(t, deps)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}

	if health.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", health.Status)
	}
	if health.Version != "1.0.0-test" {
		t.Errorf("expected version '1.0.0-test', got %q", health.Version)
	}
	if health.Routes != 3 {
		t.Errorf("expected 3 routes, got %d", health.Routes)
	}
	if health.Services != 2 {
		t.Errorf("expected 2 services, got %d", health.Services)
	}
	if health.Uptime == "" {
		t.Error("expected non-empty uptime")
	}
	if !health.ConfigValid {
		t.Error("expected config_valid to be true")
	}
}

func TestReloadEndpoint(t *testing.T) {
	deps := testDeps()
	ts := newTestServer(t, deps)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/reload", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result ReloadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if !result.OK {
		t.Error("expected ok to be true")
	}
	if result.Routes != 3 {
		t.Errorf("expected 3 routes, got %d", result.Routes)
	}
	if result.Services != 2 {
		t.Errorf("expected 2 services, got %d", result.Services)
	}
}

func TestReloadEndpointError(t *testing.T) {
	deps := testDeps()
	deps.Reload = func() *ReloadResult {
		return &ReloadResult{OK: false, Error: "route 3: action 'api' not found"}
	}
	ts := newTestServer(t, deps)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/reload", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var result ReloadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.OK {
		t.Error("expected ok to be false")
	}
	if result.Error == "" {
		t.Error("expected non-empty error")
	}
}

func TestAuthMiddlewareBlocks(t *testing.T) {
	deps := testDeps()
	ts := newTestServer(t, deps)
	defer ts.Close()

	// No auth header.
	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", resp.StatusCode)
	}

	// Wrong token.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", resp.StatusCode)
	}
}

func TestAuthMiddlewareSkipsWhenNoToken(t *testing.T) {
	deps := testDeps()
	// Override config with no token.
	origCfg := deps.GetConfig()
	origCfg.Admin.Token = ""
	deps.GetConfig = func() *config.Config { return origCfg }

	cfg := &config.AdminConfig{Listen: "127.0.0.1:0", Token: ""}
	s := New(cfg, deps)
	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 without token config, got %d", resp.StatusCode)
	}
}

func TestCertsEndpointEmpty(t *testing.T) {
	deps := testDeps()
	ts := newTestServer(t, deps)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/certs", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var certs []CertEntry
	if err := json.NewDecoder(resp.Body).Decode(&certs); err != nil {
		t.Fatal(err)
	}
	if len(certs) != 0 {
		t.Errorf("expected empty cert list, got %d entries", len(certs))
	}
}

func TestCertsEndpointWithData(t *testing.T) {
	deps := testDeps()
	expires := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	deps.CertStatus = func() []CertEntry {
		return []CertEntry{
			{Domain: "example.com", Status: "active", Expires: &expires, Issuer: "Let's Encrypt"},
			{Domain: "staging.example.com", Status: "pending"},
		}
	}

	ts := newTestServer(t, deps)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/certs", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var certs []CertEntry
	if err := json.NewDecoder(resp.Body).Decode(&certs); err != nil {
		t.Fatal(err)
	}
	if len(certs) != 2 {
		t.Fatalf("expected 2 certs, got %d", len(certs))
	}
	if certs[0].Status != "active" {
		t.Errorf("expected first cert active, got %q", certs[0].Status)
	}
	if certs[1].Status != "pending" {
		t.Errorf("expected second cert pending, got %q", certs[1].Status)
	}
}

func TestRoutesEndpoint(t *testing.T) {
	deps := testDeps()
	ts := newTestServer(t, deps)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/routes", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var routes []RouteEntry
	if err := json.NewDecoder(resp.Body).Decode(&routes); err != nil {
		t.Fatal(err)
	}

	// 2 routes in "web" + 1 in "internal" = 3 total.
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(routes))
	}

	// Routes should be sorted by service name (internal before web).
	if routes[0].Service != "internal" {
		t.Errorf("expected first service 'internal', got %q", routes[0].Service)
	}
}

func TestBalancersEndpoint(t *testing.T) {
	deps := testDeps()
	deps.BalancerInfo = func() []BalancerEntry {
		return []BalancerEntry{
			{
				Service: "web", RouteIndex: 0,
				Type: "roundrobin", Action: "proxy",
				Targets: []string{"10.0.0.1:8080", "10.0.0.2:8080"},
			},
		}
	}
	ts := newTestServer(t, deps)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/balancers", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var balancers []BalancerEntry
	if err := json.NewDecoder(resp.Body).Decode(&balancers); err != nil {
		t.Fatal(err)
	}
	if len(balancers) != 1 {
		t.Fatalf("expected 1 balancer, got %d", len(balancers))
	}
	if len(balancers[0].Targets) != 2 {
		t.Errorf("expected 2 targets, got %d", len(balancers[0].Targets))
	}
}

func TestConfigEndpointRedactsSecrets(t *testing.T) {
	deps := testDeps()
	ts := newTestServer(t, deps)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/config", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// Token should be redacted.
	if contains(bodyStr, "test-secret") {
		t.Error("config response contains unredacted token")
	}
	if !contains(bodyStr, "[REDACTED]") {
		t.Error("config response does not contain [REDACTED] marker")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	deps := testDeps()
	ts := newTestServer(t, deps)
	defer ts.Close()

	// GET on POST-only endpoint.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/reload", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}

	// POST on GET-only endpoint.
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/health", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestContentTypeJSON(t *testing.T) {
	deps := testDeps()
	ts := newTestServer(t, deps)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
