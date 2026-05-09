// Package server manages HTTP/HTTPS listener lifecycle and hot reload.
package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dortanes/prox/internal/action"
	"github.com/dortanes/prox/internal/config"
	"github.com/dortanes/prox/internal/resource"
	"github.com/dortanes/prox/internal/router"
)

const (
	shutdownTimeout = 15 * time.Second
	readTimeout     = 10 * time.Second
	writeTimeout    = 30 * time.Second
	idleTimeout     = 120 * time.Second
)

// Group manages multiple HTTP servers, one per configured service.
type Group struct {
	servers  []*managedServer
	handlers map[string]*swappableHandler // keyed by service name
}

type managedServer struct {
	name    string
	server  *http.Server
	tlsCert string
	tlsKey  string
}

// Build creates a server group from the loaded configuration.
func Build(cfg *config.Config) (*Group, error) {
	resolver := resource.NewResolver(cfg.Resources)

	hints := buildRouteHints(cfg)

	registry, err := action.Build(cfg.Actions, resolver, hints)
	if err != nil {
		return nil, fmt.Errorf("building actions: %w", err)
	}

	g := &Group{
		handlers: make(map[string]*swappableHandler),
	}

	for name, svc := range cfg.Services {
		srv, handler, err := buildServer(name, svc, registry)
		if err != nil {
			return nil, fmt.Errorf("building service %q: %w", name, err)
		}
		g.servers = append(g.servers, srv)
		g.handlers[name] = handler
	}

	return g, nil
}

// Reload atomically swaps the routing logic for all services.
// Listeners keep running — zero downtime. If the new config changes listen
// addresses or adds/removes services, those changes require a full restart.
func (g *Group) Reload(cfg *config.Config) error {
	resolver := resource.NewResolver(cfg.Resources)

	hints := buildRouteHints(cfg)

	registry, err := action.Build(cfg.Actions, resolver, hints)
	if err != nil {
		return fmt.Errorf("building actions: %w", err)
	}

	swapped := 0

	for name, svc := range cfg.Services {
		handler, ok := g.handlers[name]
		if !ok {
			slog.Warn("new service in config requires restart to take effect",
				"service", name,
			)
			continue
		}

		rt := router.New(svc.Routes)
		handler.Swap(rt, registry)
		swapped++

		slog.Info("service reloaded", "service", name)
	}

	// Warn about removed services.
	for name := range g.handlers {
		if _, ok := cfg.Services[name]; !ok {
			slog.Warn("removed service in config requires restart to take effect",
				"service", name,
			)
		}
	}

	slog.Info("reload complete", "services_swapped", swapped)
	return nil
}

func buildServer(name string, svc *config.Service, registry *action.Registry) (*managedServer, *swappableHandler, error) {
	rt := router.New(svc.Routes)
	handler := newSwappableHandler(name, rt, registry)

	srv := &http.Server{
		Addr:         svc.Listen,
		Handler:      handler,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	if svc.TLS {
		srv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			CurvePreferences: []tls.CurveID{
				tls.X25519,
				tls.CurveP256,
			},
		}
	}

	ms := &managedServer{
		name:    name,
		server:  srv,
		tlsCert: svc.TLSCert,
		tlsKey:  svc.TLSKey,
	}
	return ms, handler, nil
}

// ListenAndServe starts all servers and blocks until ctx is cancelled.
func (g *Group) ListenAndServe(ctx context.Context) error {
	errCh := make(chan error, len(g.servers))

	for _, ms := range g.servers {
		go func(ms *managedServer) {
			slog.Info("starting server",
				"service", ms.name,
				"addr", ms.server.Addr,
				"tls", ms.server.TLSConfig != nil,
			)

			var err error
			if ms.server.TLSConfig != nil {
				err = ms.server.ListenAndServeTLS(ms.tlsCert, ms.tlsKey)
			} else {
				err = ms.server.ListenAndServe()
			}

			if err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("service %q: %w", ms.name, err)
			}
		}(ms)
	}

	select {
	case err := <-errCh:
		// A server failed — shut everything down.
		g.shutdown()
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining connections...")
		g.shutdown()
		return nil
	}
}

// shutdown gracefully stops all servers with a timeout.
func (g *Group) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var wg sync.WaitGroup

	for _, ms := range g.servers {
		wg.Add(1)
		go func(ms *managedServer) {
			defer wg.Done()

			if err := ms.server.Shutdown(ctx); err != nil {
				slog.Error("shutdown error",
					"service", ms.name,
					"error", err,
				)
			} else {
				slog.Info("server stopped", "service", ms.name)
			}
		}(ms)
	}

	wg.Wait()
}

// routingSnapshot is an immutable pair of router + action registry, swapped atomically.
type routingSnapshot struct {
	router   *router.Router
	registry *action.Registry
}

// swappableHandler wraps an atomic pointer to a routingSnapshot.
type swappableHandler struct {
	name    string
	current atomic.Pointer[routingSnapshot]
}

func newSwappableHandler(name string, rt *router.Router, registry *action.Registry) *swappableHandler {
	h := &swappableHandler{name: name}
	h.current.Store(&routingSnapshot{router: rt, registry: registry})
	return h
}

// Swap atomically replaces the routing logic.
func (h *swappableHandler) Swap(rt *router.Router, registry *action.Registry) {
	h.current.Store(&routingSnapshot{router: rt, registry: registry})
}

func (h *swappableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			slog.Error("panic recovered",
				"service", h.name,
				"path", r.URL.Path,
				"panic", v,
			)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	snap := h.current.Load()

	actionName := snap.router.Match(r)
	if actionName == "" {
		http.NotFound(w, r)
		return
	}

	handler := snap.registry.Get(actionName)
	if handler == nil {
		slog.Error("action handler not found",
			"service", h.name,
			"action", actionName,
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	slog.Debug("handling request",
		"service", h.name,
		"method", r.Method,
		"path", r.URL.Path,
		"action", actionName,
	)

	handler.ServeHTTP(w, r)
}

// buildRouteHints maps action names to their route paths (for prefix stripping).
func buildRouteHints(cfg *config.Config) *action.RouteHints {
	hints := &action.RouteHints{
		PathByAction: make(map[string]string),
	}

	for _, svc := range cfg.Services {
		for _, route := range svc.Routes {
			if route.Action.Name != "" && route.Match != nil {
				hints.PathByAction[route.Action.Name] = route.Match.Path
			}
		}
	}

	return hints
}
