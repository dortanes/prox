// Package acme manages automatic TLS certificate issuance and renewal.
package acme

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"

	"github.com/dortanes/prox/internal/config"
)

// Manager wraps CertMagic to provide automatic certificate management.
type Manager struct {
	magic   *certmagic.Config
	cache   *certmagic.Cache
	domains []string
	cancel  context.CancelFunc // stops obtains and renewals started by the last ManageDomains
	mu      sync.RWMutex
}

// New creates an ACME manager from the service's ACME config.
// configDir is the directory containing the config file, used to resolve
// relative storage paths.
func New(cfg *config.ACMEConfig, configDir string) (*Manager, error) {
	storage, err := buildStorage(cfg, configDir)
	if err != nil {
		return nil, fmt.Errorf("building ACME storage: %w", err)
	}

	mgr := &Manager{}

	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(cert certmagic.Certificate) (*certmagic.Config, error) {
			return mgr.magic, nil
		},
	})

	magic := certmagic.New(cache, certmagic.Config{
		Storage: storage,
		Logger:  newSlogZapBridge(),
	})

	issuers, err := buildIssuers(cfg, magic)
	if err != nil {
		cache.Stop()
		return nil, fmt.Errorf("building ACME issuers: %w", err)
	}
	magic.Issuers = issuers

	mgr.magic = magic
	mgr.cache = cache
	mgr.domains = cfg.Domains

	storageType := cfg.StorageType
	if storageType == "" {
		storageType = "file"
	}
	slog.Debug("acme manager created",
		"storage_type", storageType,
		"issuers", len(issuers),
	)

	return mgr, nil
}

// GetCertificate returns the CertMagic GetCertificate callback
// for use in tls.Config.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return m.magic.GetCertificate(hello)
}

// ManageDomains starts certificate management for the given domains and stops
// renewing the previously managed ones that are no longer listed.
// This triggers certificate issuance for any domains that don't already
// have valid certificates in storage. Certificates are obtained in the
// background — the call returns immediately without blocking.
// Obtains still running from the previous call are cancelled; those for
// domains that stay listed start again.
func (m *Manager) ManageDomains(ctx context.Context, domains []string) error {
	keep := make(map[string]bool, len(domains))
	for _, d := range domains {
		keep[d] = true
	}
	ctx, cancel := context.WithCancel(ctx)

	m.mu.Lock()
	var dropped []certmagic.SubjectIssuer
	for _, d := range m.domains {
		if !keep[d] {
			dropped = append(dropped, certmagic.SubjectIssuer{Subject: d})
		}
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = cancel
	m.domains = domains
	m.mu.Unlock()

	if len(dropped) > 0 {
		slog.Info("acme: stopped managing certificates", "count", len(dropped))
		m.cache.RemoveManaged(dropped)
	}

	if len(domains) == 0 {
		return nil
	}

	slog.Info("acme: managing certificates",
		"domains", domains,
	)

	return m.magic.ManageAsync(ctx, domains)
}

// ManagedDomains returns the currently managed domain list.
func (m *Manager) ManagedDomains() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.domains...)
}

// CertStatus describes the current state of a managed certificate.
type CertStatus struct {
	Domain  string     `json:"domain"`
	Status  string     `json:"status"` // "active", "pending"
	Expires *time.Time `json:"expires,omitempty"`
	Issuer  string     `json:"issuer,omitempty"`
}

// CertificateStatus returns the status of all managed certificates.
// For each managed domain, it checks if a valid certificate exists
// in the CertMagic cache. Domains without a cached certificate
// are reported as "pending".
func (m *Manager) CertificateStatus() []CertStatus {
	m.mu.RLock()
	domains := append([]string(nil), m.domains...)
	m.mu.RUnlock()

	statuses := make([]CertStatus, 0, len(domains))
	for _, domain := range domains {
		statuses = append(statuses, m.certStatusForDomain(domain))
	}
	return statuses
}

// certStatusForDomain queries the TLS cache for a single domain.
func (m *Manager) certStatusForDomain(domain string) CertStatus {
	cs := CertStatus{Domain: domain, Status: "pending"}

	// Query the certificate cache directly to avoid panic during synthetic handshake.
	certs := m.cache.AllMatchingCertificates(domain)
	if len(certs) == 0 {
		return cs
	}
	cert := &certs[0]

	// Parse the leaf certificate for expiry and issuer details.
	leaf := cert.Leaf
	if leaf == nil && len(cert.Certificate.Certificate) > 0 {
		leaf, _ = x509.ParseCertificate(cert.Certificate.Certificate[0])
	}

	cs.Status = "active"
	if leaf != nil {
		expires := leaf.NotAfter
		cs.Expires = &expires
		if len(leaf.Issuer.Organization) > 0 {
			cs.Issuer = leaf.Issuer.Organization[0]
		} else if leaf.Issuer.CommonName != "" {
			cs.Issuer = leaf.Issuer.CommonName
		}
	}

	return cs
}

// Close cleanly shuts down the ACME manager and its certificate cache.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.cache.Stop()
}
