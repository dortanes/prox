package action

import (
	"bufio"
	"net"
	"net/http"
)

// responseHeaderWriter applies an action's response header changes just before
// the headers are sent, after every serving path has copied the upstream's.
type responseHeaderWriter struct {
	http.ResponseWriter
	proxy   *Proxy
	applied bool
}

func (w *responseHeaderWriter) apply() {
	if w.applied {
		return
	}
	w.applied = true
	h := w.ResponseWriter.Header()
	for _, k := range w.proxy.removeResponseHeaders {
		h.Del(k)
	}
	for k, v := range w.proxy.responseHeaders {
		h.Set(k, v)
	}
}

func (w *responseHeaderWriter) WriteHeader(status int) {
	// Informational responses precede the final headers.
	if status >= http.StatusOK || status == http.StatusSwitchingProtocols {
		w.apply()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseHeaderWriter) Write(b []byte) (int, error) {
	w.apply()
	return w.ResponseWriter.Write(b)
}

func (w *responseHeaderWriter) Flush() {
	w.apply()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *responseHeaderWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *responseHeaderWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
