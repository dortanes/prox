package action

import (
	"net/http"

	"github.com/dortanes/prox/internal/config"
	"github.com/dortanes/prox/internal/resource"
)

// Static returns a fixed response with pre-computed body, headers, and status.
type Static struct {
	status  int
	headers map[string]string
	body    []byte
}

// NewStatic creates a static response handler.
func NewStatic(act *config.Action, resolver *resource.Resolver) (*Static, error) {
	s := &Static{
		status:  act.Status,
		headers: act.Headers,
	}

	if act.BodyRef.Name != "" {
		body, err := resolver.Resolve(act.BodyRef.Name)
		if err != nil {
			return nil, err
		}
		s.body = body
	}

	return s, nil
}

func (s *Static) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for k, v := range s.headers {
		w.Header().Set(k, v)
	}

	w.WriteHeader(s.status)

	if s.body != nil {
		_, _ = w.Write(s.body)
	}
}
