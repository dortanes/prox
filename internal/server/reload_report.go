package server

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/dortanes/prox/internal/config"
)

// listenerSettings holds the service settings fixed when its listener is built.
type listenerSettings struct {
	readTimeout, writeTimeout, idleTimeout config.Duration
	maxConnections                         int
}

func listenerSettingsOf(svc *config.Service) listenerSettings {
	if svc.Config == nil {
		return listenerSettings{}
	}
	return listenerSettings{
		readTimeout:    svc.Config.ReadTimeout,
		writeTimeout:   svc.Config.WriteTimeout,
		idleTimeout:    svc.Config.IdleTimeout,
		maxConnections: svc.Config.MaxConnections,
	}
}

// acmeSettings strips the fields a reload applies from an ACME config.
func acmeSettings(acme *config.ACMEConfig) *config.ACMEConfig {
	if acme == nil {
		return nil
	}
	c := *acme
	c.Domains = nil
	if acme.DNS != nil {
		dns := *acme.DNS
		dns.Discover = false
		dns.DiscoverInterval = nil
		c.DNS = &dns
	}
	return &c
}

// restartRequired lists the differences between the config the listeners were
// built from and cfg that a reload cannot apply. Comparing against the build
// config keeps a change listed until a restart picks it up.
func restartRequired(built, cfg *config.Config) []string {
	items := []string{}
	add := func(format string, args ...any) {
		items = append(items, fmt.Sprintf(format, args...))
	}

	for name, svc := range cfg.Services {
		old, ok := built.Services[name]
		if !ok {
			add("service %q: added", name)
			continue
		}
		if old.Listen != svc.Listen {
			add("service %q: listen", name)
		}
		if old.TLS != svc.TLS {
			add("service %q: tls", name)
		}
		if !reflect.DeepEqual(old.H2, svc.H2) {
			add("service %q: h2", name)
		}
		if !reflect.DeepEqual(acmeSettings(old.ACME), acmeSettings(svc.ACME)) {
			add("service %q: acme", name)
		}
		was, now := listenerSettingsOf(old), listenerSettingsOf(svc)
		if was.readTimeout != now.readTimeout {
			add("service %q: config.read_timeout", name)
		}
		if was.writeTimeout != now.writeTimeout {
			add("service %q: config.write_timeout", name)
		}
		if was.idleTimeout != now.idleTimeout {
			add("service %q: config.idle_timeout", name)
		}
		if was.maxConnections != now.maxConnections {
			add("service %q: config.max_connections", name)
		}
	}
	for name := range built.Services {
		if _, ok := cfg.Services[name]; !ok {
			add("service %q: removed", name)
		}
	}
	if !reflect.DeepEqual(built.Admin, cfg.Admin) {
		add("admin")
	}

	var wasLog, nowLog config.LoggingConfig
	if built.Logging != nil {
		wasLog = *built.Logging
	}
	if cfg.Logging != nil {
		nowLog = *cfg.Logging
	}
	if wasLog.Level != nowLog.Level {
		add("logging.level")
	}
	if wasLog.ErrorLog != nowLog.ErrorLog {
		add("logging.error_log")
	}

	sort.Strings(items)
	return items
}
