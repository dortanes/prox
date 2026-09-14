package sdk

// Option configures a Response.
type Option func(*Response)

// ResponseOption configures a ResponseMod.
type ResponseOption func(*ResponseMod)

// Allow creates an approval response, optionally injecting headers.
func Allow(opts ...Option) *Response {
	r := &Response{Allow: true}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Deny creates a denial response with the given HTTP status and body.
func Deny(status int, body string, opts ...Option) *Response {
	r := &Response{Allow: false, Status: status, Body: body}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Fallback creates a response that tells the proxy to route the request to the configured fallback action.
func Fallback(opts ...Option) *Response {
	r := &Response{Allow: false, Fallback: true}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Drop silently closes the connection without sending any HTTP response.
func Drop() *Response {
	return &Response{Drop: true}
}

// WithHeader adds a header to the response (injected into the proxied request on allow,
// or into the HTTP response on deny).
func WithHeader(key, value string) Option {
	return func(r *Response) {
		if r.Headers == nil {
			r.Headers = make(map[string]string)
		}
		r.Headers[key] = value
	}
}

// WithSpeedLimit sets bandwidth caps in Mbps.
// Zero values mean unlimited for that direction.
// An optional groupKey aggregates bandwidth across all connections sharing
// the same key (e.g. a user ID) instead of limiting each connection independently.
func WithSpeedLimit(downloadMbps, uploadMbps float64, groupKey ...string) Option {
	return func(r *Response) {
		sl := &SpeedLimit{
			DownloadMbps: downloadMbps,
			UploadMbps:   uploadMbps,
		}
		if len(groupKey) > 0 {
			sl.GroupKey = groupKey[0]
		}
		r.SpeedLimit = sl
	}
}

// WithCleanQuery removes the query string from the proxied request URL.
func WithCleanQuery() Option {
	return func(r *Response) {
		r.CleanQuery = true
	}
}

// WithRewritePath changes the upstream request path (e.g. for removing identifiers).
func WithRewritePath(path string) Option {
	return func(r *Response) {
		r.RewritePath = path
	}
}

// WithGroup pins the request to a named target group of the route's balancer,
// overriding the group derived from the domain wildcard capture.
// Groups are the ones published via SetGroupedTargets / SetActionGroupedTargets.
// If the group holds no available target, the request falls back to the route's
// fallback action (or 502 when none is configured).
func WithGroup(group string) Option {
	return func(r *Response) {
		r.Group = group
	}
}

// WithTarget pins the request to one named upstream, overriding both the
// balanced choice and WithGroup. The address is one of the targets published
// via SetTargets / SetGroupedTargets — a member of any group, not just the one
// the domain resolves to — written the way the route's upstream template
// expects it (e.g. "10.0.0.7:8080" for "http://{target}").
//
// A pinned target that belongs to the pool keeps connection tracking accurate;
// one outside the pool is still used, but leastconn cannot account for it.
// Routes whose upstream has no "{target}" placeholder ignore it.
func WithTarget(target string) Option {
	return func(r *Response) {
		r.Target = target
	}
}

// ConnOption configures a ConnResponse.
type ConnOption func(*ConnResponse)

// AcceptConn creates an L4 connection approval.
func AcceptConn(opts ...ConnOption) *ConnResponse {
	r := &ConnResponse{Allow: true}
	for _, o := range opts {
		o(r)
	}
	return r
}

// WithConnGroup pins the connection to a named target group of the route's
// balancer, overriding the group derived from the SNI wildcard capture.
// Groups are the ones published via SetGroupedTargets / SetActionGroupedTargets.
// If the group holds no available target, the connection is closed.
func WithConnGroup(group string) ConnOption {
	return func(r *ConnResponse) {
		r.Group = group
	}
}

// WithConnTarget pins the connection to one named upstream, overriding both
// the balanced choice and WithConnGroup. The address is one of the targets
// published via SetTargets / SetGroupedTargets — a member of any group, not
// just the one the SNI resolves to. It requires the route's pass upstream to
// contain "{target}"; otherwise the address is ignored.
func WithConnTarget(target string) ConnOption {
	return func(r *ConnResponse) {
		r.Target = target
	}
}

// RejectConn creates an L4 connection denial.
func RejectConn() *ConnResponse {
	return &ConnResponse{Allow: false}
}

// ModifyResponse creates upstream response modifications.
func ModifyResponse(opts ...ResponseOption) *ResponseMod {
	m := &ResponseMod{}
	for _, o := range opts {
		o(m)
	}
	return m
}

// NoResponseMod returns an empty modification (no changes).
func NoResponseMod() *ResponseMod {
	return &ResponseMod{}
}

// WithResponseHeader adds or overrides a header in the upstream response.
func WithResponseHeader(key, value string) ResponseOption {
	return func(m *ResponseMod) {
		if m.Headers == nil {
			m.Headers = make(map[string]string)
		}
		m.Headers[key] = value
	}
}

// RemoveResponseHeader removes a header from the upstream response.
func RemoveResponseHeader(key string) ResponseOption {
	return func(m *ResponseMod) {
		m.Remove = append(m.Remove, key)
	}
}

// WithResponseStatus overrides the upstream response status code.
func WithResponseStatus(status int) ResponseOption {
	return func(m *ResponseMod) {
		m.Status = status
	}
}
