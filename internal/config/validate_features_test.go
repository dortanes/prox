package config

import (
	"strings"
	"testing"
)

func featureConfig() *Config {
	return &Config{
		Services: map[string]*Service{"web": {
			Listen: ":8080",
			Routes: []*Route{{Action: ActionRef{Name: "app"}}},
		}},
		Actions: map[string]*Action{"app": {Type: ActionTypeProxy, Upstream: "de-1:8080"}},
		Plugins: map[string]*Plugin{},
	}
}

func TestValidate_Features(t *testing.T) {
	route := func(c *Config) *Route { return c.Services["web"].Routes[0] }

	for name, tc := range map[string]struct {
		mutate func(*Config)
		want   string // empty means valid
	}{
		"strict_sni without tls": {func(c *Config) { c.Services["web"].StrictSNI = true }, "strict_sni requires tls"},
		"plugin args conflict": {func(c *Config) {
			c.Plugins["a"] = &Plugin{Path: "./p", Args: []string{"-x"}}
			c.Plugins["b"] = &Plugin{Path: "./p"}
		}, "args differ"},
		"response headers on static": {func(c *Config) {
			c.Actions["s"] = &Action{Type: ActionTypeStatic, Status: 200, ResponseHeaders: map[string]string{"A": "b"}}
		}, "response_headers"},
		"route id":         {func(c *Config) { route(c).ID = "api" }, ""},
		"numeric route id": {func(c *Config) { route(c).ID = "1" }, "must not be a number"},
		"bad route id":     {func(c *Config) { route(c).ID = "a b" }, "may only contain"},
		"duplicate route id": {func(c *Config) {
			route(c).ID = "api"
			c.Services["web"].Routes = append(c.Services["web"].Routes, &Route{ID: "api", Action: ActionRef{Name: "app"}})
		}, "duplicate id"},
		"access":                  {func(c *Config) { route(c).Access = &AccessConfig{Allow: []string{"10.0.0.0/8"}} }, ""},
		"bad access":              {func(c *Config) { route(c).Access = &AccessConfig{Deny: []string{"nope"}} }, "not an IP address"},
		"empty access":            {func(c *Config) { route(c).Access = &AccessConfig{} }, "requires allow or deny"},
		"proxy_protocol on proxy": {func(c *Config) { c.Actions["app"].ProxyProtocol = "v1" }, "requires type"},
		"bad proxy_protocol": {func(c *Config) {
			c.Actions["p"] = &Action{Type: ActionTypePass, Upstream: "de-1:8080", ProxyProtocol: "v3"}
		}, "must be"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := featureConfig()
			tc.mutate(cfg)
			err := Validate(cfg)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestRouteID(t *testing.T) {
	if got := RouteID("web", 2, &Route{}); got != "web:2" {
		t.Errorf("got %q", got)
	}
	if got := RouteID("web", 2, &Route{ID: "api"}); got != "web:api" {
		t.Errorf("got %q", got)
	}
}
