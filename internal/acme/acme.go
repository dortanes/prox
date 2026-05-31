// Package acme manages automatic TLS certificate issuance and renewal.
package acme

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"sync"

	"github.com/caddyserver/certmagic"

	"github.com/dortanes/prox/internal/config"
)

// Manager wraps CertMagic to provide automatic certificate management.
type Manager struct {
	magic   *certmagic.Config
	cache   *certmagic.Cache
	domains []string
	mu      sync.RWMutex
}

// New creates an ACME manager from the service's ACME config.
// configDir is the directory containing the config file, used to resolve
// relative storage paths.
func New(cfg *config.ACMEConfig, configDir string) (*Manager, error) {
	storagePath := resolveStoragePath(cfg.Storage, configDir)
	storage := &certmagic.FileStorage{Path: storagePath}

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

	slog.Debug("acme manager created",
		"storage", storagePath,
		"issuers", len(issuers),
	)

	return mgr, nil
}

// GetCertificate returns the CertMagic GetCertificate callback
// for use in tls.Config.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return m.magic.GetCertificate(hello)
}

// ManageDomains starts certificate management for the given domains.
// This triggers certificate issuance for any domains that don't already
// have valid certificates in storage. Certificates are obtained in the
// background — the call returns immediately without blocking.
func (m *Manager) ManageDomains(ctx context.Context, domains []string) error {
	m.mu.Lock()
	m.domains = domains
	m.mu.Unlock()

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

// Close cleanly shuts down the ACME manager and its certificate cache.
func (m *Manager) Close() {
	m.cache.Stop()
}
