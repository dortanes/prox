package acme

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/labostack/prox/internal/config"
)

// DiscoverDomains fetches all active zones from the DNS provider account
// and returns domain pairs (zone + wildcard) for certificate management.
// Uses the same API token configured for DNS-01 challenges.
func DiscoverDomains(cfg *config.ACMEDNSConfig) ([]string, error) {
	token := cfg.Token
	if token == "" {
		if envVar, ok := providerEnvVars[cfg.Provider]; ok {
			token = os.Getenv(envVar)
		}
	}
	if token == "" {
		return nil, fmt.Errorf("DNS provider token required for domain discovery")
	}

	switch cfg.Provider {
	case "cloudflare":
		return discoverCloudflareZones(token)
	default:
		return nil, fmt.Errorf("domain discovery not supported for provider %q", cfg.Provider)
	}
}

// discoverCloudflareZones fetches all active zones from the Cloudflare API
// and returns each zone as a pair: ["example.com", "*.example.com"].
func discoverCloudflareZones(token string) ([]string, error) {
	var domains []string
	page := 1

	for {
		zones, totalPages, err := fetchCloudflareZonePage(token, page)
		if err != nil {
			return nil, err
		}

		for _, zone := range zones {
			domains = append(domains, zone, "*."+zone)
		}

		if page >= totalPages {
			break
		}
		page++
	}

	slog.Info("acme: discovered domains from DNS provider",
		"count", len(domains)/2,
		"domains", domains,
	)

	return domains, nil
}

// fetchCloudflareZonePage fetches a single page of active zones.
func fetchCloudflareZonePage(token string, page int) (zones []string, totalPages int, err error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones?status=active&per_page=50&page=%d", page)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("cloudflare API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("reading cloudflare response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("cloudflare API returned %d: %s", resp.StatusCode, body)
	}

	var result cfZonesResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, 0, fmt.Errorf("parsing cloudflare response: %w", err)
	}

	if !result.Success {
		return nil, 0, fmt.Errorf("cloudflare API error: %s", result.Errors)
	}

	for _, z := range result.Result {
		zones = append(zones, z.Name)
	}

	return zones, result.ResultInfo.TotalPages, nil
}

// cfZonesResponse is the Cloudflare API response for listing zones.
type cfZonesResponse struct {
	Success    bool            `json:"success"`
	Errors     json.RawMessage `json:"errors"`
	Result     []cfZone        `json:"result"`
	ResultInfo cfResultInfo    `json:"result_info"`
}

type cfZone struct {
	Name string `json:"name"`
}

type cfResultInfo struct {
	TotalPages int `json:"total_pages"`
}
