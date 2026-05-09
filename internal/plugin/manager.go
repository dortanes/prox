package plugin

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/dortanes/prox/internal/balancer"
)

const (
	restartBaseDelay = 1 * time.Second
	restartMaxDelay  = 30 * time.Second
)

// Binding associates a route with a plugin process and its balancer.
type Binding struct {
	RouteID  string
	Plugin   string // absolute path to plugin binary
	Match    *MatchInfo
	Balancer balancer.Balancer
}

// Manager supervises plugin processes and routes push messages to balancers.
type Manager struct {
	mu        sync.Mutex
	processes map[string]*managed // keyed by absolute plugin path
	bindings  []*Binding
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// managed wraps a plugin process with restart state.
type managed struct {
	path    string
	proc    *Process
	restart int // consecutive restart count for backoff
}

// NewManager creates a plugin manager. Call Start() to spawn processes.
func NewManager() *Manager {
	return &Manager{
		processes: make(map[string]*managed),
	}
}

// Configure sets the current route-to-plugin bindings.
// Call Start() after Configure() to spawn processes.
func (m *Manager) Configure(bindings []*Binding) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bindings = bindings
}

// Start spawns all plugin processes and begins processing pushes.
func (m *Manager) Start(ctx context.Context) error {
	ctx, m.cancel = context.WithCancel(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Deduplicate plugin paths — one process per unique binary.
	needed := make(map[string]bool)
	for _, b := range m.bindings {
		needed[b.Plugin] = true
	}

	for pluginPath := range needed {
		if _, ok := m.processes[pluginPath]; ok {
			continue // already running
		}

		proc, err := startProcess(pluginPath)
		if err != nil {
			slog.Error("failed to start plugin",
				"plugin", pluginPath,
				"error", err,
			)
			continue
		}

		mg := &managed{path: pluginPath, proc: proc}
		m.processes[pluginPath] = mg

		slog.Info("plugin started",
			"plugin", filepath.Base(pluginPath),
			"pid", proc.cmd.Process.Pid,
		)

		// Send configure for all routes bound to this plugin.
		for _, b := range m.bindings {
			if b.Plugin != pluginPath {
				continue
			}
			if err := proc.Send(Request{
				Method: MethodConfigure,
				Params: ConfigureParams{
					RouteID: b.RouteID,
					Match:   b.Match,
				},
			}); err != nil {
				slog.Error("failed to configure plugin",
					"plugin", filepath.Base(pluginPath),
					"route", b.RouteID,
					"error", err,
				)
			}
		}

		// Process pushes in background.
		m.wg.Add(1)
		go func(mg *managed) {
			defer m.wg.Done()
			m.processPushes(ctx, mg)
		}(mg)
	}

	return nil
}

// Reconfigure updates bindings and reconfigures running plugins.
// New plugins are started, removed plugins are stopped.
func (m *Manager) Reconfigure(bindings []*Binding) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.bindings = bindings

	// Determine which plugins are still needed.
	needed := make(map[string]bool)
	for _, b := range bindings {
		needed[b.Plugin] = true
	}

	// Stop plugins no longer referenced.
	for path, mg := range m.processes {
		if !needed[path] {
			slog.Info("stopping unreferenced plugin",
				"plugin", filepath.Base(path),
			)
			mg.proc.Stop()
			delete(m.processes, path)
		}
	}

	// Start new plugins and reconfigure existing ones.
	for pluginPath := range needed {
		mg, exists := m.processes[pluginPath]

		if !exists {
			proc, err := startProcess(pluginPath)
			if err != nil {
				slog.Error("failed to start plugin on reconfigure",
					"plugin", pluginPath,
					"error", err,
				)
				continue
			}

			mg = &managed{path: pluginPath, proc: proc}
			m.processes[pluginPath] = mg

			slog.Info("plugin started",
				"plugin", filepath.Base(pluginPath),
				"pid", proc.cmd.Process.Pid,
			)

			// Process pushes for the new process.
			m.wg.Add(1)
			go func(mg *managed) {
				defer m.wg.Done()
				m.processPushes(context.Background(), mg)
			}(mg)
		}

		// Send fresh configure for all routes bound to this plugin.
		for _, b := range bindings {
			if b.Plugin != pluginPath {
				continue
			}
			if err := mg.proc.Send(Request{
				Method: MethodConfigure,
				Params: ConfigureParams{
					RouteID: b.RouteID,
					Match:   b.Match,
				},
			}); err != nil {
				slog.Error("failed to reconfigure plugin",
					"plugin", filepath.Base(pluginPath),
					"route", b.RouteID,
					"error", err,
				)
			}
		}
	}
}

// Stop gracefully terminates all plugin processes.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.Lock()
	for _, mg := range m.processes {
		mg.proc.Stop()
	}
	m.mu.Unlock()

	m.wg.Wait()
}

// processPushes reads from the plugin's push channel and dispatches
// set_targets to the appropriate balancer. Handles process restart on crash.
func (m *Manager) processPushes(ctx context.Context, mg *managed) {
	for {
		select {
		case <-ctx.Done():
			return
		case push, ok := <-mg.proc.Pushes():
			if !ok {
				// Plugin process died — attempt restart.
				if ctx.Err() != nil {
					return // shutting down
				}
				m.restartPlugin(ctx, mg)
				if mg.proc == nil {
					return // restart failed permanently
				}
				continue
			}

			mg.restart = 0 // reset backoff on successful message

			m.handlePush(mg, push)
		}
	}
}

// handlePush dispatches a single push message to the right balancer.
func (m *Manager) handlePush(mg *managed, push Push) {
	switch push.Method {
	case MethodSetTargets:
		m.mu.Lock()
		for _, b := range m.bindings {
			if b.Plugin == mg.path && b.RouteID == push.Params.RouteID {
				if b.Balancer != nil {
					if push.Params.Groups != nil {
						// Grouped targets — route to KeyedBalancer.
						if kb, ok := b.Balancer.(balancer.KeyedBalancer); ok {
							kb.SwapGroupedTargets(push.Params.Groups)
							total := 0
							for _, t := range push.Params.Groups {
								total += len(t)
							}
							slog.Info("plugin updated grouped targets",
								"plugin", filepath.Base(mg.path),
								"route", push.Params.RouteID,
								"groups", len(push.Params.Groups),
								"targets", total,
							)
						} else {
							slog.Warn("plugin sent grouped targets but balancer is not keyed",
								"plugin", filepath.Base(mg.path),
								"route", push.Params.RouteID,
							)
						}
					} else {
						// Flat targets.
						b.Balancer.SwapTargets(push.Params.Targets)
						slog.Info("plugin updated targets",
							"plugin", filepath.Base(mg.path),
							"route", push.Params.RouteID,
							"targets", len(push.Params.Targets),
						)
					}
				}
				break
			}
		}
		m.mu.Unlock()

	default:
		slog.Debug("unknown plugin push method",
			"plugin", filepath.Base(mg.path),
			"method", push.Method,
		)
	}
}

// restartPlugin attempts to restart a crashed plugin with exponential backoff.
func (m *Manager) restartPlugin(ctx context.Context, mg *managed) {
	mg.restart++
	delay := restartBaseDelay * time.Duration(1<<min(mg.restart-1, 4))
	if delay > restartMaxDelay {
		delay = restartMaxDelay
	}

	slog.Warn("plugin process exited, restarting",
		"plugin", filepath.Base(mg.path),
		"attempt", mg.restart,
		"delay", delay,
	)

	select {
	case <-ctx.Done():
		mg.proc = nil
		return
	case <-time.After(delay):
	}

	proc, err := startProcess(mg.path)
	if err != nil {
		slog.Error("failed to restart plugin",
			"plugin", mg.path,
			"attempt", mg.restart,
			"error", err,
		)
		mg.proc = nil
		return
	}

	mg.proc = proc

	slog.Info("plugin restarted",
		"plugin", filepath.Base(mg.path),
		"pid", proc.cmd.Process.Pid,
		"attempt", mg.restart,
	)

	// Re-send configure for all bound routes.
	m.mu.Lock()
	for _, b := range m.bindings {
		if b.Plugin != mg.path {
			continue
		}
		_ = proc.Send(Request{
			Method: MethodConfigure,
			Params: ConfigureParams{
				RouteID: b.RouteID,
				Match:   b.Match,
			},
		})
	}
	m.mu.Unlock()
}
