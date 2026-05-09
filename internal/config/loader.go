package config

import (
	"fmt"
	"os"

	"github.com/titanous/json5"
)

// LoadFile reads, parses, and validates a JSON5 configuration file.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	return Load(data)
}

// Load parses raw JSON5 bytes into a validated Config.
func Load(data []byte) (*Config, error) {
	var cfg Config
	if err := json5.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	normalize(&cfg)

	if err := Validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// normalize hoists inline action/resource definitions into the top-level maps.
func normalize(cfg *Config) {
	if cfg.Actions == nil {
		cfg.Actions = make(map[string]*Action)
	}
	if cfg.Resources == nil {
		cfg.Resources = make(map[string]*Resource)
	}

	// Normalize inline actions in routes.
	for svcName, svc := range cfg.Services {
		for i, route := range svc.Routes {
			if route.Action.IsInline() {
				name := fmt.Sprintf("_inline_%s_%d", svcName, i)
				cfg.Actions[name] = route.Action.Inline
				svc.Routes[i].Action = ActionRef{Name: name}
			}
		}
	}

	// Normalize inline resources in actions.
	for actName, act := range cfg.Actions {
		if act.BodyRef.IsInline() {
			name := fmt.Sprintf("_inline_%s_body", actName)
			cfg.Resources[name] = act.BodyRef.Inline
			act.BodyRef = ResourceRef{Name: name}
		}
	}
}
