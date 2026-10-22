package plugin

import (
	"context"
	"log/slog"
	"time"

	"github.com/dortanes/prox/internal/conntrack"
)

// startUsageLocked begins periodic on_usage reports for a plugin.
// Must be called with m.mu held.
func (m *Manager) startUsageLocked(mg *managed) {
	ctx, cancel := context.WithCancel(context.Background())
	mg.meter = m.sessions.NewMeter()
	mg.stopUsage = cancel
	interval := m.usageIntervalLocked(mg)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.reportUsage(ctx, mg, interval)
	}()
}

// endUsageLocked stops a plugin's usage reports. With flush set, the usage not
// reported yet is sent first. Must be called with m.mu held.
func (m *Manager) endUsageLocked(mg *managed, flush bool) {
	if mg.meter == nil {
		return
	}
	mg.stopUsage()
	if flush {
		m.sendUsage(mg.name, mg.meter, mg.caller, m.collectUsageLocked(mg))
	}
	m.sessions.RemoveMeter(mg.meter)
	mg.meter, mg.stopUsage = nil, nil
}

func (m *Manager) reportUsage(ctx context.Context, mg *managed, interval time.Duration) {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		m.mu.Lock()
		meter, caller := mg.meter, mg.caller
		usage := m.collectUsageLocked(mg)
		interval = m.usageIntervalLocked(mg)
		m.mu.Unlock()

		m.sendUsage(mg.name, meter, caller, usage)
		timer.Reset(interval)
	}
}

// collectUsageLocked takes the plugin's unreported usage on the routes it is
// bound to. Must be called with m.mu held.
func (m *Manager) collectUsageLocked(mg *managed) map[conntrack.Key]conntrack.Bytes {
	if mg.meter == nil {
		return nil
	}
	usage := m.sessions.Collect(mg.meter)
	bound := make(map[string]bool)
	for _, b := range m.bindings {
		if b.Plugin == mg.path {
			bound[b.RouteID] = true
		}
	}
	for k := range usage {
		if !bound[k.RouteID] {
			delete(usage, k)
		}
	}
	return usage
}

// usageIntervalLocked returns the plugin's report interval.
// Must be called with m.mu held.
func (m *Manager) usageIntervalLocked(mg *managed) time.Duration {
	for _, b := range m.bindings {
		if b.Plugin == mg.path && b.UsageInterval > 0 {
			return b.UsageInterval
		}
	}
	return defaultUsageInterval
}

// sendUsage delivers usage as on_usage frames. Usage the plugin cannot
// receive goes back to its meter and is reported again next time.
func (m *Manager) sendUsage(plugin string, meter *conntrack.Meter, caller *Caller, usage map[conntrack.Key]conntrack.Bytes) {
	if len(usage) == 0 {
		return
	}
	if caller == nil {
		m.sessions.Restore(meter, usage)
		return
	}

	report := UsageReport{TimestampMs: time.Now().UnixMilli()}
	batch := make(map[conntrack.Key]conntrack.Bytes)
	size := 0
	send := func() {
		frame, err := MarshalEnvelope(HookTypeUsage, &report)
		if err == nil {
			err = caller.Fire(frame)
		}
		if err != nil {
			slog.Debug("usage report failed",
				"plugin", plugin,
				"sessions", len(report.Sessions),
				"err", err,
			)
			m.sessions.Restore(meter, batch)
		}
		report.Sessions = report.Sessions[:0]
		batch = make(map[conntrack.Key]conntrack.Bytes)
		size = 0
	}

	for k, b := range usage {
		report.Sessions = append(report.Sessions, SessionUsage{
			RouteID: k.RouteID,
			Session: k.Session,
			BytesRx: b.Rx,
			BytesTx: b.Tx,
		})
		batch[k] = b
		size += len(k.RouteID) + len(k.Session) + 32
		if size >= maxUsageFrame {
			send()
		}
	}
	if len(report.Sessions) > 0 {
		send()
	}
}
