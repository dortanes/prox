package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"

	"github.com/dortanes/prox/internal/config"
)

func TestManageDomains_StopsManagingDroppedDomains(t *testing.T) {
	m, err := New(&config.ACMEConfig{Email: "admin@example.com", Storage: t.TempDir()}, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	domains := []string{"example.com", "example.org"}
	for _, d := range domains {
		storeTestCertificate(t, m.magic, d)
		if _, err := m.magic.CacheManagedCertificate(ctx, d); err != nil {
			t.Fatalf("caching %s: %v", d, err)
		}
	}
	m.domains = domains

	if err := m.ManageDomains(ctx, []string{"example.com"}); err != nil {
		t.Fatalf("ManageDomains: %v", err)
	}

	if len(m.cache.AllMatchingCertificates("example.com")) == 0 {
		t.Error("kept domain lost its certificate")
	}
	if n := len(m.cache.AllMatchingCertificates("example.org")); n != 0 {
		t.Errorf("dropped domain still has %d managed certificates", n)
	}
}

// blockingIssuer holds every issuance open until its context ends.
type blockingIssuer struct {
	started chan struct{}
	stopped chan struct{}
}

func (b *blockingIssuer) IssuerKey() string { return "blocking" }

func (b *blockingIssuer) Issue(ctx context.Context, _ *x509.CertificateRequest) (*certmagic.IssuedCertificate, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case b.stopped <- struct{}{}:
	default:
	}
	return nil, ctx.Err()
}

func TestManageDomains_CancelsObtainForDroppedDomain(t *testing.T) {
	m, err := New(&config.ACMEConfig{Email: "admin@example.com", Storage: t.TempDir()}, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	iss := &blockingIssuer{started: make(chan struct{}, 1), stopped: make(chan struct{}, 1)}
	m.magic.Issuers = []certmagic.Issuer{iss}

	ctx := context.Background()
	if err := m.ManageDomains(ctx, []string{"example.org"}); err != nil {
		t.Fatalf("ManageDomains: %v", err)
	}
	select {
	case <-iss.started:
	case <-time.After(10 * time.Second):
		t.Fatal("obtain did not start")
	}

	if err := m.ManageDomains(ctx, nil); err != nil {
		t.Fatalf("ManageDomains: %v", err)
	}
	select {
	case <-iss.stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("obtain for the dropped domain kept running")
	}
}

// storeTestCertificate writes a self-signed certificate where certmagic loads
// managed certificates from.
func storeTestCertificate(t *testing.T, magic *certmagic.Config, domain string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	issuerKey := magic.Issuers[0].IssuerKey()
	files := map[string][]byte{
		certmagic.StorageKeys.SiteCert(issuerKey, domain):       pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		certmagic.StorageKeys.SitePrivateKey(issuerKey, domain): pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		certmagic.StorageKeys.SiteMeta(issuerKey, domain):       []byte("{}"),
	}
	for k, v := range files {
		if err := magic.Storage.Store(context.Background(), k, v); err != nil {
			t.Fatal(err)
		}
	}
}

// --- resolveStoragePath ---

func TestResolveStoragePath_EmptyDefault(t *testing.T) {
	got := resolveStoragePath("", "/etc/prox")
	want := "/etc/prox/acme"
	if got != want {
		t.Errorf("resolveStoragePath(\"\", \"/etc/prox\") = %q, want %q", got, want)
	}
}

func TestResolveStoragePath_Absolute(t *testing.T) {
	got := resolveStoragePath("/custom/path", "/etc/prox")
	want := "/custom/path"
	if got != want {
		t.Errorf("resolveStoragePath(\"/custom/path\", \"/etc/prox\") = %q, want %q", got, want)
	}
}

func TestResolveStoragePath_Relative(t *testing.T) {
	got := resolveStoragePath("certs", "/etc/prox")
	want := "/etc/prox/certs"
	if got != want {
		t.Errorf("resolveStoragePath(\"certs\", \"/etc/prox\") = %q, want %q", got, want)
	}
}

// --- resolveCA ---

func TestResolveCA(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", certmagic.LetsEncryptProductionCA},
		{"letsencrypt", certmagic.LetsEncryptProductionCA},
		{"production", certmagic.LetsEncryptProductionCA},
		{"staging", certmagic.LetsEncryptStagingCA},
		{"zerossl", certmagic.ZeroSSLProductionCA},
		{"https://custom.ca/dir", "https://custom.ca/dir"},
	}

	for _, tt := range tests {
		t.Run("input="+tt.input, func(t *testing.T) {
			got := resolveCA(tt.input)
			if got != tt.want {
				t.Errorf("resolveCA(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- buildDNSSolver ---

func TestBuildDNSSolver_TokenInConfig(t *testing.T) {
	cfg := &config.ACMEDNSConfig{
		Provider: "cloudflare",
		Token:    "my-api-token",
	}

	solver, err := buildDNSSolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if solver == nil {
		t.Fatal("expected non-nil solver")
	}

	// Verify the provider is a Cloudflare provider with the correct token.
	provider, ok := solver.DNSManager.DNSProvider.(*cloudflare.Provider)
	if !ok {
		t.Fatalf("expected *cloudflare.Provider, got %T", solver.DNSManager.DNSProvider)
	}
	if provider.APIToken != "my-api-token" {
		t.Errorf("APIToken = %q, want %q", provider.APIToken, "my-api-token")
	}
}

func TestBuildDNSSolver_TokenFromEnv(t *testing.T) {
	t.Setenv("CF_DNS_API_TOKEN", "test-token")

	cfg := &config.ACMEDNSConfig{
		Provider: "cloudflare",
		Token:    "",
	}

	solver, err := buildDNSSolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if solver == nil {
		t.Fatal("expected non-nil solver")
	}

	provider, ok := solver.DNSManager.DNSProvider.(*cloudflare.Provider)
	if !ok {
		t.Fatalf("expected *cloudflare.Provider, got %T", solver.DNSManager.DNSProvider)
	}
	if provider.APIToken != "test-token" {
		t.Errorf("APIToken = %q, want %q", provider.APIToken, "test-token")
	}
}

func TestBuildDNSSolver_NoTokenNoEnv(t *testing.T) {
	t.Setenv("CF_DNS_API_TOKEN", "")

	cfg := &config.ACMEDNSConfig{
		Provider: "cloudflare",
		Token:    "",
	}

	_, err := buildDNSSolver(cfg)
	if err == nil {
		t.Fatal("expected error when no token is available")
	}
}

func TestBuildDNSSolver_UnknownProvider(t *testing.T) {
	cfg := &config.ACMEDNSConfig{
		Provider: "unknown-provider",
		Token:    "",
	}

	_, err := buildDNSSolver(cfg)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestBuildDNSSolver_Resolvers(t *testing.T) {
	cfg := &config.ACMEDNSConfig{
		Provider:  "cloudflare",
		Token:     "my-api-token",
		Resolvers: []string{"1.1.1.1:53", "8.8.8.8:53"},
	}

	solver, err := buildDNSSolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if solver == nil {
		t.Fatal("expected non-nil solver")
	}

	// Verify resolvers are passed through to the DNSManager.
	if len(solver.DNSManager.Resolvers) != 2 {
		t.Fatalf("expected 2 resolvers, got %d", len(solver.DNSManager.Resolvers))
	}
	if solver.DNSManager.Resolvers[0] != "1.1.1.1:53" {
		t.Errorf("Resolvers[0] = %q, want %q", solver.DNSManager.Resolvers[0], "1.1.1.1:53")
	}
	if solver.DNSManager.Resolvers[1] != "8.8.8.8:53" {
		t.Errorf("Resolvers[1] = %q, want %q", solver.DNSManager.Resolvers[1], "8.8.8.8:53")
	}
}

func TestBuildDNSSolver_NoResolvers(t *testing.T) {
	cfg := &config.ACMEDNSConfig{
		Provider: "cloudflare",
		Token:    "my-api-token",
	}

	solver, err := buildDNSSolver(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// When no resolvers are configured, default public DNS resolvers are used.
	if len(solver.DNSManager.Resolvers) != 2 {
		t.Fatalf("expected 2 default resolvers, got %d: %v", len(solver.DNSManager.Resolvers), solver.DNSManager.Resolvers)
	}
	if solver.DNSManager.Resolvers[0] != "1.1.1.1:53" {
		t.Errorf("Resolvers[0] = %q, want %q", solver.DNSManager.Resolvers[0], "1.1.1.1:53")
	}
	if solver.DNSManager.Resolvers[1] != "8.8.8.8:53" {
		t.Errorf("Resolvers[1] = %q, want %q", solver.DNSManager.Resolvers[1], "8.8.8.8:53")
	}
}
