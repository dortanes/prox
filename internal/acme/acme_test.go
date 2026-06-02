package acme

import (
	"testing"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"

	"github.com/labostack/prox/internal/config"
)

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
