package plugin

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dortanes/prox/internal/conntrack"
	"github.com/vmihailenco/msgpack/v5"
)

// fakePlugin serves the plugin socket protocol: request hooks get the given
// verdicts, usage frames are forwarded to reports.
type fakePlugin struct {
	path    string
	auth    AuthorizeResult
	conn    ConnResult
	reports chan UsageReport
}

func startFakePlugin(t *testing.T, auth AuthorizeResult, conn ConnResult) *fakePlugin {
	t.Helper()
	dir, err := os.MkdirTemp("", "prox")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	fp := &fakePlugin{
		path:    filepath.Join(dir, "p.sock"),
		auth:    auth,
		conn:    conn,
		reports: make(chan UsageReport, 64),
	}
	ln, err := net.Listen("unix", fp.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go fp.serve(c)
		}
	}()
	return fp
}

func (fp *fakePlugin) serve(c net.Conn) {
	defer c.Close()
	for {
		raw, err := readFrame(c)
		if err != nil {
			return
		}
		var env Envelope
		if err := msgpack.Unmarshal(raw, &env); err != nil {
			return
		}
		var reply any
		switch env.Hook {
		case HookTypeRequest:
			reply = fp.auth
		case HookTypeConnect:
			reply = fp.conn
		case HookTypeUsage:
			var r UsageReport
			if err := msgpack.Unmarshal(env.Data, &r); err == nil {
				fp.reports <- r
			}
			continue
		default:
			continue
		}
		out, _ := msgpack.Marshal(reply)
		if err := writeFrame(c, out); err != nil {
			return
		}
	}
}

func managerWithCallers(routeID, hook string, callers ...*Caller) *Manager {
	m := NewManager()
	m.index.Store(&hookIndex{
		callers:  map[string]map[string][]*Caller{routeID: {hook: callers}},
		timeouts: map[string]time.Duration{},
	})
	return m
}

func TestOnRequest_LastSessionWins(t *testing.T) {
	first := startFakePlugin(t, AuthorizeResult{Allow: true, Session: "acct-1"}, ConnResult{})
	second := startFakePlugin(t, AuthorizeResult{Allow: true}, ConnResult{})
	third := startFakePlugin(t, AuthorizeResult{Allow: true, Session: "acct-3"}, ConnResult{})
	m := managerWithCallers("web:0", HookOnRequest,
		NewCaller(first.path, 1, time.Second),
		NewCaller(second.path, 1, time.Second),
		NewCaller(third.path, 1, time.Second),
	)

	res, err := m.OnRequest(context.Background(), "web:0", &RequestInfo{RouteID: "web:0"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Session != "acct-3" {
		t.Fatalf("session = %q, want acct-3", res.Session)
	}
}

func TestOnConnect_Session(t *testing.T) {
	fp := startFakePlugin(t, AuthorizeResult{}, ConnResult{Allow: true, Session: "acct-1"})
	m := managerWithCallers("tcp:0", HookOnConnect, NewCaller(fp.path, 1, time.Second))

	res, err := m.OnConnect(context.Background(), "tcp:0", &ConnInfo{RouteID: "tcp:0"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Session != "acct-1" {
		t.Fatalf("session = %q, want acct-1", res.Session)
	}
}

func TestHandlePush_DropSessions(t *testing.T) {
	m := NewManager()
	dropped := map[string]bool{}
	m.sessions.Open("web:0", "acct-1", nil, func() { dropped["acct-1"] = true })
	m.sessions.Open("web:0", "acct-2", nil, func() { dropped["acct-2"] = true })

	m.handlePush(&managed{name: "auth"}, Push{
		Method: MethodDropSessions,
		Params: PushParams{Sessions: []string{"acct-1", "unknown"}},
	})

	if !dropped["acct-1"] || dropped["acct-2"] {
		t.Fatalf("dropped = %v, want only acct-1", dropped)
	}
}

func TestUsageReports(t *testing.T) {
	fp := startFakePlugin(t, AuthorizeResult{}, ConnResult{})
	m := NewManager()
	m.bindings = []*Binding{
		{Plugin: "/auth", RouteID: "web:0", UsageInterval: 20 * time.Millisecond},
		{Plugin: "/other", RouteID: "web:1"},
	}
	mg := &managed{name: "auth", path: "/auth", caller: NewCaller(fp.path, 1, time.Second)}
	m.mu.Lock()
	m.startUsageLocked(mg)
	m.mu.Unlock()

	bound, unbound := &conntrack.Counter{}, &conntrack.Counter{}
	conn := m.sessions.Open("web:0", "acct-1", bound, func() {})
	m.sessions.Open("web:1", "acct-1", unbound, func() {})
	bound.AddRx(100)
	bound.AddTx(1000)
	unbound.AddRx(1)

	var total conntrack.Bytes
	live := false
	deadline := time.After(5 * time.Second)
	for total.Tx < 1000 {
		select {
		case r := <-fp.reports:
			for _, s := range r.Sessions {
				if s.RouteID != "web:0" || s.Session != "acct-1" {
					t.Fatalf("unexpected entry %+v", s)
				}
				total.Rx += s.BytesRx
				total.Tx += s.BytesTx
			}
			live = true
		case <-deadline:
			t.Fatal("no usage report for a live connection")
		}
	}
	if !live || total != (conntrack.Bytes{Rx: 100, Tx: 1000}) {
		t.Fatalf("live total = %+v", total)
	}

	bound.AddTx(5)
	m.sessions.Close(conn)
	m.mu.Lock()
	m.endUsageLocked(mg, true)
	m.mu.Unlock()
	m.wg.Wait()

	for {
		select {
		case r := <-fp.reports:
			for _, s := range r.Sessions {
				total.Rx += s.BytesRx
				total.Tx += s.BytesTx
			}
			continue
		case <-time.After(200 * time.Millisecond):
		}
		break
	}
	if total != (conntrack.Bytes{Rx: 100, Tx: 1005}) {
		t.Fatalf("final total = %+v, want rx 100 tx 1005", total)
	}
}

func TestUsageReports_RestoredWhenUndelivered(t *testing.T) {
	fp := startFakePlugin(t, AuthorizeResult{}, ConnResult{})
	m := NewManager()
	m.bindings = []*Binding{{Plugin: "/auth", RouteID: "web:0"}}
	meter := m.sessions.NewMeter()
	c := &conntrack.Counter{}
	m.sessions.Open("web:0", "acct-1", c, func() {})
	c.AddTx(42)

	usage := m.sessions.Collect(meter)
	m.sendUsage("auth", meter, nil, usage)
	m.sendUsage("auth", meter, NewCaller(fp.path, 1, time.Second), m.sessions.Collect(meter))

	select {
	case r := <-fp.reports:
		if len(r.Sessions) != 1 || r.Sessions[0].BytesTx != 42 {
			t.Fatalf("report = %+v, want the restored 42 bytes", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("restored usage not delivered")
	}
}
