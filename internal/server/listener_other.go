//go:build !unix && !linux && !darwin && !freebsd && !openbsd && !netbsd

package server

import (
	"context"
	"net"
)

// reusePortListen falls back to a standard TCP listener on platforms that do not support SO_REUSEPORT (e.g. Windows).
func reusePortListen(network, addr string) (net.Listener, error) {
	var lc net.ListenConfig
	return lc.Listen(context.Background(), network, addr)
}
