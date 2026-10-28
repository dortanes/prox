package tests

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dortanes/prox/internal/plugin"
	"github.com/vmihailenco/msgpack/v5"
)

// probeEventsEnv names the file the probe plugin appends its events to. When
// it is set, the test binary runs as the probe plugin instead of the tests.
const probeEventsEnv = "PROX_TEST_PROBE_EVENTS"

func TestMain(m *testing.M) {
	if path := os.Getenv(probeEventsEnv); path != "" {
		if err := runProbePlugin(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

// probeEvent is one line of the probe's events file.
type probeEvent struct {
	Request    *plugin.RequestInfo    `json:"request,omitempty"`
	Disconnect *plugin.DisconnectInfo `json:"disconnect,omitempty"`
	Usage      *plugin.UsageReport    `json:"usage,omitempty"`
}

// runProbePlugin speaks the plugin protocol: on_request takes the session key
// from X-Session and drops the sessions listed in X-Drop; on_connect takes it
// from the first SNI label; on_request, on_disconnect and on_usage are recorded.
func runProbePlugin(eventsPath string) error {
	events, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer events.Close()

	dir, err := os.MkdirTemp("", "probe")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer ln.Close()

	var mu sync.Mutex
	stdout := json.NewEncoder(os.Stdout)
	send := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		_ = stdout.Encode(v)
	}
	record := func(e probeEvent) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(events).Encode(e)
	}

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveProbe(c, send, record)
		}
	}()

	ready := false
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil || req.Method != plugin.MethodConfigure {
			continue
		}
		send(map[string]string{"result": "ok"})
		if !ready {
			ready = true
			send(plugin.Push{Method: plugin.MethodReady, Params: plugin.PushParams{
				Socket: sock,
				Hooks:  []string{plugin.HookOnRequest, plugin.HookOnConnect, plugin.HookOnDisconnect, plugin.HookOnUsage},
			}})
		}
	}
	return scanner.Err()
}

func serveProbe(c net.Conn, send func(any), record func(probeEvent)) {
	defer c.Close()
	for {
		raw, err := readProbeFrame(c)
		if err != nil {
			return
		}
		var env plugin.Envelope
		if msgpack.Unmarshal(raw, &env) != nil {
			return
		}

		var reply any
		switch env.Hook {
		case plugin.HookTypeRequest:
			var req plugin.RequestInfo
			_ = msgpack.Unmarshal(env.Data, &req)
			record(probeEvent{Request: &req})
			if drop := req.Headers["X-Drop"]; drop != "" {
				send(plugin.Push{Method: plugin.MethodDropSessions, Params: plugin.PushParams{
					Sessions: strings.Split(drop, ","),
				}})
				reply = plugin.AuthorizeResult{Status: 202}
				break
			}
			reply = plugin.AuthorizeResult{Allow: true, Session: req.Headers["X-Session"]}
		case plugin.HookTypeConnect:
			var conn plugin.ConnInfo
			_ = msgpack.Unmarshal(env.Data, &conn)
			label, _, _ := strings.Cut(conn.Domain, ".")
			reply = plugin.ConnResult{Allow: true, Session: label}
		case plugin.HookTypeDisconnect:
			var info plugin.DisconnectInfo
			if msgpack.Unmarshal(env.Data, &info) == nil {
				record(probeEvent{Disconnect: &info})
			}
			continue
		case plugin.HookTypeUsage:
			var report plugin.UsageReport
			if msgpack.Unmarshal(env.Data, &report) == nil {
				record(probeEvent{Usage: &report})
			}
			continue
		default:
			continue
		}

		out, _ := msgpack.Marshal(reply)
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(out)))
		if _, err := c.Write(append(prefix[:], out...)); err != nil {
			return
		}
	}
}

func readProbeFrame(r io.Reader) ([]byte, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return nil, err
	}
	buf := make([]byte, binary.BigEndian.Uint32(prefix[:]))
	_, err := io.ReadFull(r, buf)
	return buf, err
}

// probeEvents reads the events the probe has recorded so far.
func probeEvents(t *testing.T, path string) []probeEvent {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read probe events: %v", err)
	}
	var events []probeEvent
	for _, line := range bytes.Split(data, []byte("\n")) {
		var e probeEvent
		if json.Unmarshal(line, &e) == nil {
			events = append(events, e)
		}
	}
	return events
}
