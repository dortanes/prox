package plugin

import (
	"net"
	"testing"
	"time"
)

func TestCaller_CloseWhileReturningConn(t *testing.T) {
	for range 200 {
		c := NewCaller("unused.sock", 1, time.Second)
		client, server := net.Pipe()
		done := make(chan struct{})
		go func() {
			c.putConn(client)
			close(done)
		}()
		c.Close()
		<-done
		server.Close()

		if conn, err := c.getConn(); err == nil {
			conn.Close()
			t.Fatal("closed caller handed out a pooled connection")
		}
	}
}
