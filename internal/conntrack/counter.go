// Package conntrack counts the bytes live connections move and tracks the
// connections a plugin tagged with a session key, so they can be metered and
// dropped while they are still open.
package conntrack

import (
	"io"
	"net"
	"sync/atomic"
)

// Counter counts the bytes one connection or stream moves on the client side.
// It is updated as bytes flow, so it can be read while the connection is open.
type Counter struct {
	rx atomic.Int64 // client → prox
	tx atomic.Int64 // prox → client
}

// AddRx adds n bytes sent by the client.
func (c *Counter) AddRx(n int64) { c.rx.Add(n) }

// AddTx adds n bytes sent to the client.
func (c *Counter) AddTx(n int64) { c.tx.Add(n) }

// Rx returns the bytes sent by the client so far.
func (c *Counter) Rx() int64 { return c.rx.Load() }

// Tx returns the bytes sent to the client so far.
func (c *Counter) Tx() int64 { return c.tx.Load() }

// Conn wraps a client connection so bytes read from it count as sent by the
// client and bytes written to it as sent to the client. A nil Counter returns
// conn itself, which keeps the kernel copy fast paths of *net.TCPConn.
func (c *Counter) Conn(conn net.Conn) net.Conn {
	if c == nil {
		return conn
	}
	return &countingConn{Conn: conn, c: c}
}

// RxBody wraps a request body so every byte read from it counts as sent by
// the client.
func (c *Counter) RxBody(rc io.ReadCloser) io.ReadCloser {
	return &countingBody{rc: rc, c: c}
}

type countingConn struct {
	net.Conn
	c *Counter
}

func (cc *countingConn) Read(p []byte) (int, error) {
	n, err := cc.Conn.Read(p)
	cc.c.AddRx(int64(n))
	return n, err
}

func (cc *countingConn) Write(p []byte) (int, error) {
	n, err := cc.Conn.Write(p)
	cc.c.AddTx(int64(n))
	return n, err
}

// CloseWrite half-closes the connection when the underlying one supports it.
func (cc *countingConn) CloseWrite() error {
	if hc, ok := cc.Conn.(interface{ CloseWrite() error }); ok {
		return hc.CloseWrite()
	}
	return nil
}

type countingBody struct {
	rc io.ReadCloser
	c  *Counter
}

func (cb *countingBody) Read(p []byte) (int, error) {
	n, err := cb.rc.Read(p)
	cb.c.AddRx(int64(n))
	return n, err
}

func (cb *countingBody) Close() error { return cb.rc.Close() }
