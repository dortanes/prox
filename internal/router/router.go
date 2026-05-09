// Package router implements first-match HTTP request routing.
//
// Routes are evaluated in order. The first route whose path pattern,
// domain pattern, and method filter match the incoming request is selected.
// If no route matches, the router returns nil and the caller should respond with 404.
package router

import (
	"context"
	"net/http"
	"strings"

	"github.com/dortanes/prox/internal/config"
)

// ctxKey is an unexported type for context keys to avoid collisions.
type ctxKey struct{}

// matchResultKey is the context key for MatchResult.
var matchResultKey = ctxKey{}

// MatchResult holds data captured during route matching, available to handlers.
type MatchResult struct {
	Action        string // resolved action name
	DomainPattern string // the pattern from config, e.g. "*.myapp.dev"
	MatchDomain   string // captured wildcard value(s), e.g. "sub" for *.myapp.dev
	MatchPath     string // the path pattern, e.g. "/api/*"
	Domain        string // actual request host (no port)
	Path          string // actual request path
}

// GetMatchResult retrieves the MatchResult from request context.
func GetMatchResult(r *http.Request) *MatchResult {
	v, _ := r.Context().Value(matchResultKey).(*MatchResult)
	return v
}

// Router holds compiled routes for a single service.
type Router struct {
	routes []*compiledRoute
}

// compiledRoute is an optimized, pre-processed representation of a config route.
type compiledRoute struct {
	action  string
	path    string
	isWild  bool            // true for wildcard paths like "/api/*"
	prefix  string          // for wildcards: the prefix before "*"
	methods map[string]bool // nil means "all methods"

	// Domain matching — segment-based glob.
	// Each "*" matches exactly one domain label.
	// Examples: "*.myapp.dev", "test.*.myapp.dev", "*.*.myapp.dev"
	domain         string   // original pattern (for MatchResult)
	domainSegments []string // nil = match all hosts
}

// New compiles a list of config routes into a Router.
func New(routes []*config.Route) *Router {
	compiled := make([]*compiledRoute, 0, len(routes))

	for _, r := range routes {
		cr := &compiledRoute{
			action: r.Action.Name,
			path:   r.Match.Path,
			domain: r.Match.Domain,
		}

		// Pre-compute wildcard prefix.
		if strings.HasSuffix(r.Match.Path, "/*") {
			cr.isWild = true
			cr.prefix = strings.TrimSuffix(r.Match.Path, "*")
		}

		// Pre-compute domain segments for glob matching.
		if r.Match.Domain != "" {
			cr.domainSegments = strings.Split(strings.ToLower(r.Match.Domain), ".")
		}

		// Pre-compute method set for O(1) lookup.
		if len(r.Match.Methods) > 0 {
			cr.methods = make(map[string]bool, len(r.Match.Methods))
			for _, m := range r.Match.Methods {
				cr.methods[strings.ToUpper(m)] = true
			}
		}

		compiled = append(compiled, cr)
	}

	return &Router{routes: compiled}
}

// Match finds the first route matching the given request.
// Returns the action name (empty if no match) and injects MatchResult into context.
func (rt *Router) Match(r *http.Request) (*http.Request, string) {
	host := stripPort(r.Host)

	for _, route := range rt.routes {
		ok, captures := route.matchDomain(host)
		if !ok {
			continue
		}
		if !route.matchPath(r.URL.Path) {
			continue
		}
		if !route.matchMethod(r.Method) {
			continue
		}

		result := &MatchResult{
			Action:        route.action,
			DomainPattern: route.domain,
			MatchDomain:   strings.Join(captures, "."),
			MatchPath:     route.path,
			Domain:        host,
			Path:          r.URL.Path,
		}
		ctx := context.WithValue(r.Context(), matchResultKey, result)

		return r.WithContext(ctx), route.action
	}
	return r, ""
}

// matchPath checks if the request path matches this route's pattern.
// An empty path matches all paths.
func (cr *compiledRoute) matchPath(reqPath string) bool {
	if cr.path == "" {
		return true
	}
	if cr.isWild {
		return strings.HasPrefix(reqPath, cr.prefix)
	}
	return reqPath == cr.path
}

// matchMethod checks if the request method is allowed.
// A nil method set means all methods are accepted.
func (cr *compiledRoute) matchMethod(method string) bool {
	if cr.methods == nil {
		return true
	}
	return cr.methods[method]
}

// matchDomain checks if the request host matches this route's domain pattern.
// Returns (matched, captures) where captures are the values matched by "*" segments.
// nil domainSegments matches all hosts (returns true, nil).
func (cr *compiledRoute) matchDomain(host string) (bool, []string) {
	if cr.domainSegments == nil {
		return true, nil
	}

	hostSegments := strings.Split(strings.ToLower(host), ".")

	// Segment count must match exactly.
	if len(hostSegments) != len(cr.domainSegments) {
		return false, nil
	}

	var captures []string
	for i, pat := range cr.domainSegments {
		if pat == "*" {
			captures = append(captures, hostSegments[i])
			continue
		}
		if hostSegments[i] != pat {
			return false, nil
		}
	}

	return true, captures
}

// stripPort removes the port from a host string.
func stripPort(host string) string {
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		return host[:idx]
	}
	return host
}
