// Package plugin implements external plugin process management.
//
// Plugins are external executables that communicate with prox over
// stdin/stdout using line-delimited JSON messages. They can dynamically
// update route state (e.g. balancer targets) based on external data sources.
package plugin

// Method constants for the JSON-RPC protocol.
const (
	MethodConfigure  = "configure"
	MethodSetTargets = "set_targets"
)

// Request is a message sent from prox to a plugin.
type Request struct {
	Method string      `json:"method"`
	Params interface{} `json:"params,omitempty"`
}

// ConfigureParams is sent to the plugin on startup and after reload.
type ConfigureParams struct {
	RouteID string     `json:"route_id"`
	Match   *MatchInfo `json:"match,omitempty"`
}

// MatchInfo provides the route's match criteria to the plugin.
type MatchInfo struct {
	Domain string `json:"domain,omitempty"`
	Path   string `json:"path,omitempty"`
}

// Response is a simple acknowledgement from a plugin.
type Response struct {
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Push is a message sent from a plugin to prox.
type Push struct {
	Method string      `json:"method"`
	Params PushParams  `json:"params"`
}

// PushParams carries the data for a push message.
type PushParams struct {
	RouteID string              `json:"route_id"`
	Targets []string            `json:"targets,omitempty"`
	Groups  map[string][]string `json:"groups,omitempty"`
}
