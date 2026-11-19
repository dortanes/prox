package server

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	proxacme "github.com/dortanes/prox/internal/acme"
	"github.com/dortanes/prox/internal/dispatcher"
)

// acmeTLSProto is the ALPN protocol of TLS-ALPN-01 challenges, which only the
// ACME manager can answer.
const acmeTLSProto = "acme-tls/1"

var (
	errNoCertificate = errors.New("no certificate for server name")
	errSNIDropped    = errors.New("server name dropped by route")
)

// loadedCert is a certificate pair and the file it came from.
type loadedCert struct {
	cert   *tls.Certificate
	source string
}

// certSet is an immutable collection of manual certificates, indexed by name.
type certSet struct {
	certs  []loadedCert
	byName map[string][]*tls.Certificate
}

func newCertSet(certs []loadedCert) *certSet {
	s := &certSet{certs: certs, byName: make(map[string][]*tls.Certificate)}
	for _, c := range certs {
		for _, name := range certNames(c.cert) {
			key := strings.ToLower(name)
			s.byName[key] = append(s.byName[key], c.cert)
		}
	}
	return s
}

// certNames returns the names a certificate covers. The common name counts
// only for certificates without subject alternative names.
func certNames(cert *tls.Certificate) []string {
	leaf := cert.Leaf
	names := slices.Clone(leaf.DNSNames)
	for _, ip := range leaf.IPAddresses {
		names = append(names, ip.String())
	}
	if len(names) == 0 && leaf.Subject.CommonName != "" {
		names = append(names, leaf.Subject.CommonName)
	}
	return names
}

// match returns the certificate covering the hello's server name, preferring
// an exact name over a wildcard and one the client can use over one it cannot.
func (s *certSet) match(hello *tls.ClientHelloInfo) *tls.Certificate {
	name := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	if name == "" {
		return nil
	}
	candidates := s.byName[name]
	if len(candidates) == 0 {
		if _, rest, ok := strings.Cut(name, "."); ok {
			candidates = s.byName["*."+rest]
		}
	}
	for _, c := range candidates {
		if hello.SupportsCertificate(c) == nil {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return nil
}

// certStore selects the certificate for each handshake. Its contents are
// swapped atomically on reload; established connections are unaffected.
type certStore struct {
	set    atomic.Pointer[certSet]
	drops  atomic.Pointer[[]*dispatcher.Route]
	strict atomic.Bool
	acme   func(*tls.ClientHelloInfo) (*tls.Certificate, error) // nil without ACME
}

func (s *certStore) update(set *certSet, drops []*dispatcher.Route, strict bool) {
	s.set.Store(set)
	s.drops.Store(&drops)
	s.strict.Store(strict)
}

// getCertificate is the tls.Config callback. Manual certificates win over ACME.
func (s *certStore) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if s.acme != nil && slices.Contains(hello.SupportedProtos, acmeTLSProto) {
		return s.acme(hello)
	}
	if s.dropped(hello.ServerName) {
		return nil, errSNIDropped
	}

	set := s.set.Load()
	if cert := set.match(hello); cert != nil {
		return cert, nil
	}
	if s.acme != nil {
		return s.acme(hello)
	}
	if !s.strict.Load() && len(set.certs) > 0 {
		return set.certs[0].cert, nil
	}
	return nil, fmt.Errorf("%w %q", errNoCertificate, hello.ServerName)
}

// dropped reports whether the first route matching the server name is a drop
// route that applies to the whole domain.
func (s *certStore) dropped(serverName string) bool {
	routes := *s.drops.Load()
	if len(routes) == 0 || serverName == "" {
		return false
	}
	name := strings.ToLower(serverName)
	for _, r := range routes {
		if r.Matches(name) {
			return r.IsDrop && r.WholeDomain
		}
	}
	return false
}

// status describes every loaded certificate.
func (s *certStore) status(now time.Time) []proxacme.CertStatus {
	certs := s.set.Load().certs
	result := make([]proxacme.CertStatus, 0, len(certs))
	for _, c := range certs {
		leaf := c.cert.Leaf
		names := certNames(c.cert)
		expires := leaf.NotAfter
		cs := proxacme.CertStatus{
			Status:  "active",
			Expires: &expires,
			Issuer:  proxacme.IssuerName(leaf),
			Names:   names,
			Serial:  leaf.SerialNumber.Text(16),
			Source:  c.source,
		}
		if len(names) > 0 {
			cs.Domain = names[0]
		}
		if now.After(expires) {
			cs.Status = "expired"
		}
		result = append(result, cs)
	}
	return result
}

// loadCertSet loads the certificates of a service. A directory may be empty,
// and so may a directory that does not exist yet; certificates can arrive
// later through a reload. Without tls_key, tls_cert names a directory.
func loadCertSet(certPath, keyPath string) (*certSet, error) {
	if certPath == "" {
		return newCertSet(nil), nil
	}
	info, err := os.Stat(certPath)
	if errors.Is(err, fs.ErrNotExist) && keyPath == "" {
		slog.Warn("certificate directory does not exist", "path", certPath)
		return newCertSet(nil), nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", certPath, err)
	}

	if info.IsDir() {
		certs, err := loadCertificatesFromDir(certPath)
		if err != nil {
			return nil, err
		}
		return newCertSet(certs), nil
	}

	cert, err := loadKeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	return newCertSet([]loadedCert{cert}), nil
}

func loadKeyPair(certFile, keyFile string) (loadedCert, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return loadedCert{}, fmt.Errorf("loading keypair (%s, %s): %w", certFile, keyFile, err)
	}
	return loadedCert{cert: &cert, source: certFile}, nil
}

// loadCertificatesFromDir scans a directory for certificate+key pairs, in
// file name order. Matches by basename: "example.com.crt" pairs with
// "example.com.key". Supported extensions: .crt, .pem (cert) and .key (key).
func loadCertificatesFromDir(dir string) ([]loadedCert, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading cert directory %q: %w", dir, err)
	}

	certFiles := make(map[string]string) // basename (no ext) → full path
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".crt" || ext == ".pem" {
			certFiles[strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))] = filepath.Join(dir, e.Name())
		}
	}

	bases := make([]string, 0, len(certFiles))
	for base := range certFiles {
		bases = append(bases, base)
	}
	sort.Strings(bases)

	certs := make([]loadedCert, 0, len(bases))
	for _, base := range bases {
		certFile := certFiles[base]
		keyFile := filepath.Join(dir, base+".key")
		if _, err := os.Stat(keyFile); err != nil {
			return nil, fmt.Errorf("no matching key file for %q (expected %s)", certFile, keyFile)
		}
		cert, err := loadKeyPair(certFile, keyFile)
		if err != nil {
			return nil, err
		}
		slog.Debug("loaded certificate pair", "cert", certFile, "key", keyFile)
		certs = append(certs, cert)
	}
	return certs, nil
}
