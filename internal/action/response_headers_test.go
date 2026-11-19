package action

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dortanes/prox/internal/config"
)

func TestProxy_ResponseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "backend")
		w.Header().Set("X-Frame-Options", "ALLOW")
		w.Header().Set("X-Kept", "yes")
	}))
	defer upstream.Close()

	for name, act := range map[string]config.Action{
		"fast path": {},
		"full path": {Headers: map[string]string{"X-Up": "1"}},
		"stream":    {Stream: true},
	} {
		t.Run(name, func(t *testing.T) {
			act.Type = config.ActionTypeProxy
			act.Upstream = upstream.URL
			act.ResponseHeaders = map[string]string{"X-Frame-Options": "DENY"}
			act.RemoveResponseHeaders = []string{"Server"}
			p, err := NewProxy(&act, nil)
			if err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			h := rec.Result().Header
			if got := h.Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("X-Frame-Options = %q, want DENY", got)
			}
			if got := h.Get("Server"); got != "" {
				t.Errorf("Server = %q, want it removed", got)
			}
			if got := h.Get("X-Kept"); got != "yes" {
				t.Errorf("X-Kept = %q, want yes", got)
			}
		})
	}
}
