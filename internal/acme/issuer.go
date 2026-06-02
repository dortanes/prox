package acme

import (
	"github.com/caddyserver/certmagic"

	"github.com/labostack/prox/internal/config"
)

// buildIssuers creates CertMagic issuers from the ACME config.
// When CAs are specified, each CA gets its own issuer (tried in order).
// The magic config is required by NewACMEIssuer to properly initialize
// internal state.
func buildIssuers(cfg *config.ACMEConfig, magic *certmagic.Config) ([]certmagic.Issuer, error) {
	cas := cfg.CAs
	if len(cas) == 0 {
		cas = []string{cfg.CA} // single CA (possibly empty → default)
	}

	var issuers []certmagic.Issuer
	for _, ca := range cas {
		template := certmagic.ACMEIssuer{
			CA:     resolveCA(ca),
			Email:  cfg.Email,
			Agreed: true,
		}

		// Configure challenge solver.
		if cfg.Challenge == "dns" && cfg.DNS != nil {
			solver, err := buildDNSSolver(cfg.DNS)
			if err != nil {
				return nil, err
			}
			template.DNS01Solver = solver
		}

		issuer := certmagic.NewACMEIssuer(magic, template)
		issuers = append(issuers, issuer)
	}

	return issuers, nil
}

// resolveCA expands shorthand CA names to full directory URLs.
func resolveCA(ca string) string {
	switch ca {
	case "", "letsencrypt", "production":
		return certmagic.LetsEncryptProductionCA
	case "staging":
		return certmagic.LetsEncryptStagingCA
	case "zerossl":
		return certmagic.ZeroSSLProductionCA
	default:
		return ca // assume full URL
	}
}
