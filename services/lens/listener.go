package lens

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"syscall"
)

// ListenWithFallback binds a TCP listener on host:preferred. If the port is
// taken, it tries up to `attempts` consecutive ports (preferred+1, preferred+2, ...).
// attempts=0 disables fallback (use when the user explicitly chose the port).
//
// An empty host defaults to "localhost" (loopback). This is a fail-closed
// default — the lens dashboard exposes session history with no
// authentication, and an empty-host listen ":port" would bind on all
// interfaces (0.0.0.0 / ::) and expose that data to anyone on the same
// network. Callers wanting a non-loopback bind must say so explicitly.
func ListenWithFallback(host string, preferred, attempts int) (net.Listener, error) {
	if host == "" {
		host = "localhost"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(preferred))
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	if attempts <= 0 || !isAddrInUse(err) {
		return nil, err
	}
	for i := 1; i <= attempts; i++ {
		addr := net.JoinHostPort(host, strconv.Itoa(preferred+i))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		if !isAddrInUse(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("no free port in range %d..%d", preferred, preferred+attempts)
}

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
