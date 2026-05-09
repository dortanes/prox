// Package resource resolves named resources into their content.
package resource

import (
	"encoding/json"
	"fmt"

	"github.com/dortanes/prox/internal/config"
)

// Resolver looks up resource content by name.
type Resolver struct {
	resources map[string]*config.Resource
}

// NewResolver creates a Resolver from the config's resource map.
func NewResolver(resources map[string]*config.Resource) *Resolver {
	if resources == nil {
		resources = make(map[string]*config.Resource)
	}
	return &Resolver{resources: resources}
}

// Resolve returns the content of a named resource.
func (r *Resolver) Resolve(name string) ([]byte, error) {
	res, ok := r.resources[name]
	if !ok {
		return nil, fmt.Errorf("resource %q not found", name)
	}

	if res.Text != "" {
		return []byte(res.Text), nil
	}

	if res.JSON != nil {
		return json.Marshal(res.JSON)
	}

	return nil, fmt.Errorf("resource %q has no content", name)
}
