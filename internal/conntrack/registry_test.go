package conntrack

import (
	"bytes"
	"io"
	"net"
	"testing"
)

func TestMeter_DeltasAcrossCollects(t *testing.T) {
	r := NewRegistry()
	m := r.NewMeter()
	c := &Counter{}
	conn := r.Open("web:0", "alice", c, func() {})

	c.AddRx(100)
	c.AddTx(1000)
	got := r.Collect(m)
	want := Bytes{Rx: 100, Tx: 1000}
	if got[Key{"web:0", "alice"}] != want {
		t.Fatalf("first collect = %+v, want %+v", got, want)
	}

	if got := r.Collect(m); len(got) != 0 {
		t.Fatalf("idle collect = %+v, want empty", got)
	}

	c.AddRx(5)
	r.Close(conn)
	c.AddTx(7) // bytes after Close are not reported
	got = r.Collect(m)
	if got[Key{"web:0", "alice"}] != (Bytes{Rx: 5}) {
		t.Fatalf("collect after close = %+v, want rx 5", got)
	}
	if got := r.Collect(m); len(got) != 0 {
		t.Fatalf("collect after flush = %+v, want empty", got)
	}
}

func TestMeter_AggregatesBySessionAndRoute(t *testing.T) {
	r := NewRegistry()
	m := r.NewMeter()
	a, b, c := &Counter{}, &Counter{}, &Counter{}
	r.Open("web:0", "alice", a, func() {})
	r.Open("web:0", "alice", b, func() {})
	r.Open("web:1", "alice", c, func() {})
	a.AddRx(1)
	b.AddRx(2)
	c.AddTx(3)

	got := r.Collect(m)
	if got[Key{"web:0", "alice"}] != (Bytes{Rx: 3}) || got[Key{"web:1", "alice"}] != (Bytes{Tx: 3}) || len(got) != 2 {
		t.Fatalf("collect = %+v", got)
	}
}

func TestMeter_IndependentMeters(t *testing.T) {
	r := NewRegistry()
	fast, slow := r.NewMeter(), r.NewMeter()
	c := &Counter{}
	conn := r.Open("web:0", "alice", c, func() {})
	key := Key{"web:0", "alice"}

	c.AddRx(10)
	if got := r.Collect(fast)[key]; got.Rx != 10 {
		t.Fatalf("fast first = %+v", got)
	}
	c.AddRx(10)
	if got := r.Collect(fast)[key]; got.Rx != 10 {
		t.Fatalf("fast second = %+v", got)
	}
	r.Close(conn)
	if got := r.Collect(slow)[key]; got.Rx != 20 {
		t.Fatalf("slow = %+v, want the full 20 bytes", got)
	}
	if got := r.Collect(fast); len(got) != 0 {
		t.Fatalf("fast after close = %+v, want empty", got)
	}
}

func TestMeter_SkipsUncountedConnections(t *testing.T) {
	r := NewRegistry()
	m := r.NewMeter()
	conn := r.Open("web:0", "alice", nil, func() {})
	r.Close(conn)
	if got := r.Collect(m); len(got) != 0 {
		t.Fatalf("collect = %+v, want empty", got)
	}
}

func TestMeter_Restore(t *testing.T) {
	r := NewRegistry()
	m := r.NewMeter()
	c := &Counter{}
	r.Open("web:0", "alice", c, func() {})
	c.AddTx(42)

	r.Restore(m, r.Collect(m))
	c.AddTx(1)
	if got := r.Collect(m)[Key{"web:0", "alice"}]; got.Tx != 43 {
		t.Fatalf("collect after restore = %+v, want tx 43", got)
	}
}

func TestRegistry_DropOnlyMatchingSessions(t *testing.T) {
	r := NewRegistry()
	dropped := map[string]int{}
	open := func(session string) *Conn {
		return r.Open("web:0", session, nil, func() { dropped[session]++ })
	}
	open("alice")
	open("alice")
	open("bob")
	gone := open("carol")
	r.Close(gone)

	if n := r.Drop("alice", "carol", "nobody"); n != 2 {
		t.Fatalf("Drop returned %d, want 2", n)
	}
	if dropped["alice"] != 2 || dropped["bob"] != 0 || dropped["carol"] != 0 {
		t.Fatalf("dropped = %v", dropped)
	}
}

func TestCounter_Wrappers(t *testing.T) {
	c := &Counter{}
	client, peer := net.Pipe()
	defer peer.Close()
	counted := c.Conn(client)

	go func() {
		_, _ = peer.Write(make([]byte, 300))
		_, _ = io.ReadFull(peer, make([]byte, 20))
		peer.Close()
	}()
	if _, err := io.ReadFull(counted, make([]byte, 300)); err != nil {
		t.Fatal(err)
	}
	if _, err := counted.Write(make([]byte, 20)); err != nil {
		t.Fatal(err)
	}
	body := c.RxBody(io.NopCloser(bytes.NewReader(make([]byte, 5))))
	if _, err := io.ReadAll(body); err != nil {
		t.Fatal(err)
	}
	if c.Rx() != 305 || c.Tx() != 20 {
		t.Fatalf("rx=%d tx=%d, want 305/20", c.Rx(), c.Tx())
	}

	var nilCounter *Counter
	if nilCounter.Conn(client) != client {
		t.Fatal("nil counter must return the connection unchanged")
	}
}
