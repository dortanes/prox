// Package router implements first-match HTTP request routing.
//
// Routes are evaluated in order. The first route whose path pattern and
// method filter match the incoming request is selected. If no route
// matches, the router returns nil and the caller should respond with 404.
package router

import (
	"net/http"
	"strings"

	"github.com/dortanes/prox/internal/config"
)

// Router holds compiled routes for a single service.
type Router struct {
	routes []*compiledRoute
}

// compiledRoute is an optimized, pre-processed representation of a config route.
type compiledRoute struct {
	action  string
	path    string
	isWild  bool          // true for wildcard paths like "/api/*"
	prefix  string        // for wildcards: the prefix before "*"
	methods map[string]bool // nil means "all methods"
}

// New compiles a list of config routes into a Router.
func New(routes []*config.Route) *Router {
	compiled := make([]*compiledRoute, 0, len(routes))

	for _, r := range routes {
		cr := &compiledRoute{
			action: r.Action.Name,
			path:   r.Match.Path,
		}

		// Pre-compute wildcard prefix.
		if strings.HasSuffix(r.Match.Path, "/*") {
			cr.isWild = true
			cr.prefix = strings.TrimSuffix(r.Match.Path, "*")
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
// Returns the action name, or empty string if no route matches.
func (rt *Router) Match(r *http.Request) string {
	for _, route := range rt.routes {
		if !route.matchPath(r.URL.Path) {
			continue
		}
		if !route.matchMethod(r.Method) {
			continue
		}
		return route.action
	}
	return ""
}

// matchPath checks if the request path matches this route's pattern.
func (cr *compiledRoute) matchPath(reqPath string) bool {
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
