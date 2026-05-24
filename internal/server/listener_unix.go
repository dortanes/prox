//go:build unix || linux || darwin || freebsd || openbsd || netbsd

package server

import (
	"context"
	"net"
	"syscall"
)

// reusePortListen creates a net.Listener with the SO_REUSEPORT socket option enabled.
func reusePortListen(network, addr string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var opterr error
			err := c.Control(func(fd uintptr) {
				opterr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
			})
			if err != nil {
				return err
			}
			return opterr
		},
	}
	return lc.Listen(context.Background(), network, addr)
}
