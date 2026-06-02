// Package admin provides an optional HTTP management API for prox.
//
// When configured, the admin server exposes endpoints for health checks,
// config reload, certificate status, and runtime inspection. It is
// designed to be zero-overhead when disabled — no goroutines, listeners,
// or allocations are created unless the admin block is present in config.
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/labostack/prox/internal/config"
)

// --- Response types ---

// HealthResponse is the JSON body for GET /api/health.
type HealthResponse struct {
	Status      string `json:"status"`
	Version     string `json:"version"`
	Uptime      string `json:"uptime"`
	Routes      int    `json:"routes"`
	Services    int    `json:"services"`
	ConfigValid bool   `json:"config_valid"`
}

// ReloadResult is the JSON body for POST /api/reload.
type ReloadResult struct {
	OK       bool   `json:"ok"`
	Routes   int    `json:"routes,omitempty"`
	Services int    `json:"services,omitempty"`
	Error    string `json:"error,omitempty"`
}

// CertEntry is one item in the GET /api/certs response.
type CertEntry struct {
	Domain  string     `json:"domain"`
	Status  string     `json:"status"`
	Expires *time.Time `json:"expires,omitempty"`
	Issuer  string     `json:"issuer,omitempty"`
}

// RouteEntry is one item in the GET /api/routes response.
type RouteEntry struct {
	Service  string     `json:"service"`
	Index    int        `json:"index"`
	Match    *MatchInfo `json:"match,omitempty"`
	Action   string     `json:"action"`
	Balancer string     `json:"balancer,omitempty"`
	Plugins  []string   `json:"plugins,omitempty"`
}

// MatchInfo describes a route's matching criteria.
type MatchInfo struct {
	Domain  string   `json:"domain,omitempty"`
	Path    string   `json:"path,omitempty"`
	Methods []string `json:"methods,omitempty"`
}

// ServiceEntry is one item in the GET /api/services response.
type ServiceEntry struct {
	Name   string `json:"name"`
	Listen string `json:"listen"`
	TLS    bool   `json:"tls"`
	ACME   bool   `json:"acme"`
	Routes int    `json:"routes"`
}

// PluginEntry is one item in the GET /api/plugins response.
type PluginEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// BalancerEntry is one item in the GET /api/balancers response.
type BalancerEntry struct {
	Service    string   `json:"service"`
	RouteIndex int      `json:"route_index"`
	Type       string   `json:"type"`
	Action     string   `json:"action"`
	Targets    []string `json:"targets"`
}

// --- Server ---

// Deps provides the admin server with access to runtime state.
// All fields are callbacks to avoid tight coupling with other packages.
type Deps struct {
	StartTime    time.Time
	Version      string
	GetConfig    func() *config.Config
	Reload       func() *ReloadResult
	RouteCount   func() int
	ServiceInfo  func() []ServiceEntry
	CertStatus   func() []CertEntry
	PluginInfo   func() []PluginEntry
	BalancerInfo func() []BalancerEntry
}

// Server is the admin API HTTP server.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
	deps       Deps
	token      string
	unixPath   string // non-empty when using a Unix socket (for cleanup)
}

// New creates an admin server bound to the given address.
// The listen address may be a TCP address ("127.0.0.1:9090") or a
// Unix socket path prefixed with "unix://" ("unix:///var/run/prox.sock").
func New(cfg *config.AdminConfig, deps Deps) *Server {
	s := &Server{
		deps:  deps,
		token: cfg.Token,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.requireMethod(http.MethodGet, s.handleHealth))
	mux.HandleFunc("/api/reload", s.requireMethod(http.MethodPost, s.handleReload))
	mux.HandleFunc("/api/certs", s.requireMethod(http.MethodGet, s.handleCerts))
	mux.HandleFunc("/api/routes", s.requireMethod(http.MethodGet, s.handleRoutes))
	mux.HandleFunc("/api/services", s.requireMethod(http.MethodGet, s.handleServices))
	mux.HandleFunc("/api/plugins", s.requireMethod(http.MethodGet, s.handlePlugins))
	mux.HandleFunc("/api/balancers", s.requireMethod(http.MethodGet, s.handleBalancers))
	mux.HandleFunc("/api/config", s.requireMethod(http.MethodGet, s.handleConfig))

	s.httpServer = &http.Server{
		Handler:      s.authMiddleware(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

// ListenAndServe starts the admin API server. It blocks until the
// server is shut down or an error occurs.
func (s *Server) ListenAndServe() error {
	ln, err := s.listen()
	if err != nil {
		return fmt.Errorf("admin: listen %s: %w", s.listenAddr(), err)
	}
	s.listener = ln

	slog.Info("admin API started", "addr", s.listenAddr())

	if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("admin: serve: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the admin server.
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Debug("admin API shutting down")
	err := s.httpServer.Shutdown(ctx)
	if s.unixPath != "" {
		os.Remove(s.unixPath)
	}
	return err
}

// --- Listener ---

func (s *Server) listen() (net.Listener, error) {
	addr := s.listenAddr()

	if strings.HasPrefix(addr, "unix://") {
		path := strings.TrimPrefix(addr, "unix://")
		s.unixPath = path

		// Remove stale socket file if it exists.
		if _, err := os.Stat(path); err == nil {
			os.Remove(path)
		}

		ln, err := net.Listen("unix", path)
		if err != nil {
			return nil, err
		}

		// Restrict socket permissions (owner + group only).
		if err := os.Chmod(path, 0660); err != nil {
			ln.Close()
			return nil, fmt.Errorf("chmod socket: %w", err)
		}

		return ln, nil
	}

	return net.Listen("tcp", addr)
}

func (s *Server) listenAddr() string {
	cfg := s.deps.GetConfig()
	if cfg != nil && cfg.Admin != nil {
		return cfg.Admin.Listen
	}
	return ""
}

// --- Middleware ---

// authMiddleware checks the Bearer token on every request.
// If no token is configured, all requests are allowed through.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+s.token {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "unauthorized",
				})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireMethod wraps a handler to reject requests with the wrong HTTP method.
func (s *Server) requireMethod(method string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": fmt.Sprintf("method %s not allowed, use %s", r.Method, method),
			})
			return
		}
		handler(w, r)
	}
}

// --- Handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	cfg := s.deps.GetConfig()
	configValid := cfg != nil && config.Validate(cfg) == nil

	resp := HealthResponse{
		Status:      "ok",
		Version:     s.deps.Version,
		Uptime:      time.Since(s.deps.StartTime).Truncate(time.Second).String(),
		Routes:      s.deps.RouteCount(),
		Services:    len(cfg.Services),
		ConfigValid: configValid,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleReload(w http.ResponseWriter, _ *http.Request) {
	result := s.deps.Reload()

	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (s *Server) handleCerts(w http.ResponseWriter, _ *http.Request) {
	certs := s.deps.CertStatus()
	if certs == nil {
		certs = []CertEntry{}
	}
	writeJSON(w, http.StatusOK, certs)
}

func (s *Server) handleRoutes(w http.ResponseWriter, _ *http.Request) {
	cfg := s.deps.GetConfig()
	if cfg == nil {
		writeJSON(w, http.StatusOK, []RouteEntry{})
		return
	}

	var entries []RouteEntry

	// Sort service names for deterministic output.
	names := sortedKeys(cfg.Services)

	for _, svcName := range names {
		svc := cfg.Services[svcName]
		for i, route := range svc.Routes {
			entry := RouteEntry{
				Service: svcName,
				Index:   i,
				Action:  route.Action.Name,
				Plugins: collectPlugins(svc, route, cfg),
			}

			if route.Match != nil {
				entry.Match = &MatchInfo{
					Domain:  route.Match.Domain,
					Path:    route.Match.Path,
					Methods: route.Match.Methods,
				}
			}

			if route.Balancer != nil {
				entry.Balancer = string(route.Balancer.Type)
			}

			entries = append(entries, entry)
		}
	}

	if entries == nil {
		entries = []RouteEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleServices(w http.ResponseWriter, _ *http.Request) {
	services := s.deps.ServiceInfo()
	if services == nil {
		services = []ServiceEntry{}
	}
	writeJSON(w, http.StatusOK, services)
}

func (s *Server) handlePlugins(w http.ResponseWriter, _ *http.Request) {
	plugins := s.deps.PluginInfo()
	if plugins == nil {
		plugins = []PluginEntry{}
	}
	writeJSON(w, http.StatusOK, plugins)
}

func (s *Server) handleBalancers(w http.ResponseWriter, _ *http.Request) {
	balancers := s.deps.BalancerInfo()
	if balancers == nil {
		balancers = []BalancerEntry{}
	}
	writeJSON(w, http.StatusOK, balancers)
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.deps.GetConfig()
	if cfg == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}

	sanitized := sanitizeConfig(cfg)
	writeJSON(w, http.StatusOK, sanitized)
}

// --- Helpers ---

// writeJSON marshals v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		slog.Debug("admin: json encode error", "err", err)
	}
}

// collectPlugins merges plugins from all levels: route, service, and action.
func collectPlugins(svc *config.Service, route *config.Route, cfg *config.Config) []string {
	seen := make(map[string]bool)
	var result []string

	add := func(names []string) {
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				result = append(result, n)
			}
		}
	}

	add(svc.Plugins)
	add(route.Plugins)

	if route.Action.Name != "" {
		if act, ok := cfg.Actions[route.Action.Name]; ok {
			add(act.Plugins)
		}
	}

	return result
}

// sanitizeConfig creates a JSON-safe copy of the config with secrets redacted.
func sanitizeConfig(cfg *config.Config) any {
	// Marshal to JSON, then unmarshal to a generic map for redaction.
	data, err := json.Marshal(cfg)
	if err != nil {
		return map[string]string{"error": "failed to serialize config"}
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]string{"error": "failed to process config"}
	}

	// Redact known secret fields.
	redactSecrets(m)
	return m
}

// redactSecrets walks a JSON map and replaces known secret fields with "[REDACTED]".
func redactSecrets(m map[string]any) {
	secretKeys := map[string]bool{
		"token": true,
	}

	for k, v := range m {
		if secretKeys[k] {
			if s, ok := v.(string); ok && s != "" {
				m[k] = "[REDACTED]"
			}
			continue
		}
		switch val := v.(type) {
		case map[string]any:
			redactSecrets(val)
		case []any:
			for _, item := range val {
				if sub, ok := item.(map[string]any); ok {
					redactSecrets(sub)
				}
			}
		}
	}
}

// sortedKeys returns the keys of a map in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
