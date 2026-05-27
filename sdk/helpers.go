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

// WithSpeedLimit sets per-connection bandwidth caps in Mbps.
// Zero values mean unlimited for that direction.
func WithSpeedLimit(downloadMbps, uploadMbps float64) Option {
	return func(r *Response) {
		r.SpeedLimit = &SpeedLimit{
			DownloadMbps: downloadMbps,
			UploadMbps:   uploadMbps,
		}
	}
}

// WithCleanQuery removes the query string from the proxied request URL.
func WithCleanQuery() Option {
	return func(r *Response) {
		r.CleanQuery = true
	}
}

// AcceptConn creates an L4 connection approval.
func AcceptConn() *ConnResponse {
	return &ConnResponse{Allow: true}
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
