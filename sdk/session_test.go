package sdk

import (
	"bytes"
	"encoding/json"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

func TestWithSession(t *testing.T) {
	resp := Allow(WithSession("acct-1"), WithSpeedLimit(10, 5, "acct-1"))
	if resp.Session != "acct-1" {
		t.Fatalf("session = %q, want acct-1", resp.Session)
	}
	if resp.SpeedLimit.GroupKey != "acct-1" {
		t.Fatalf("group key = %q, want acct-1", resp.SpeedLimit.GroupKey)
	}
	if conn := AcceptConn(WithConnSession("acct-2")); conn.Session != "acct-2" {
		t.Fatalf("conn session = %q, want acct-2", conn.Session)
	}
}

func TestSessionOmittedFromWireWhenEmpty(t *testing.T) {
	data, err := msgpack.Marshal(Allow())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := msgpack.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["se"]; ok {
		t.Fatal("empty session must not be encoded")
	}
}

func TestOnUsageAdvertisedAndDelivered(t *testing.T) {
	p := New()
	got := make(chan *UsageReport, 1)
	p.OnUsage(func(r *UsageReport) { got <- r })
	if !slices.Contains(p.hooks(), "on_usage") {
		t.Fatalf("hooks = %v, want on_usage", p.hooks())
	}

	server, client := net.Pipe()
	defer client.Close()
	go handleConn(server, p)

	want := UsageReport{
		TimestampMs: 1700000000000,
		Sessions:    []SessionUsage{{RouteID: "web:0", Session: "acct-1", BytesRx: 10, BytesTx: 20}},
	}
	data, _ := msgpack.Marshal(&want)
	frame, _ := msgpack.Marshal(&Envelope{Hook: HookUsage, Data: data})
	if err := writeFrame(client, frame); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-got:
		if r.TimestampMs != want.TimestampMs || !slices.Equal(r.Sessions, want.Sessions) {
			t.Fatalf("report = %+v, want %+v", r, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("usage report not delivered")
	}
}

func TestDropSessionsPush(t *testing.T) {
	var out bytes.Buffer
	p := New()
	p.stdout = json.NewEncoder(&out)
	p.DropSessions("acct-1", "acct-2")

	var msg struct {
		Method string `json:"method"`
		Params struct {
			Sessions []string `json:"sessions"`
		} `json:"params"`
	}
	if err := json.Unmarshal(out.Bytes(), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Method != "drop_sessions" || !slices.Equal(msg.Params.Sessions, []string{"acct-1", "acct-2"}) {
		t.Fatalf("push = %s", out.String())
	}
}
