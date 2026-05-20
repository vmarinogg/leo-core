package lens

import (
	"net"
	"strings"
	"testing"
)

// TestListenWithFallback_EmptyHostBindsLoopbackOnly locks the
// security-critical default: an empty host argument must NOT bind on
// 0.0.0.0 / [::] (all interfaces). The lens dashboard has no auth;
// exposing it on any non-loopback interface would leak session
// history to anyone on the same network.
func TestListenWithFallback_EmptyHostBindsLoopbackOnly(t *testing.T) {
	ln, err := ListenWithFallback("", 0, 0) // port 0 = OS-assigned
	if err != nil {
		t.Fatalf("ListenWithFallback: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	if !addr.IP.IsLoopback() {
		t.Errorf("empty host bound to %s, want loopback", addr.IP)
	}
	// Belt-and-braces string check: 0.0.0.0 is not loopback per
	// IsLoopback(), but if a future Go change ever shifted that
	// classification, this catches the regression.
	if strings.HasPrefix(addr.String(), "0.0.0.0:") || strings.HasPrefix(addr.String(), "[::]:") {
		t.Errorf("empty host bound to all-interfaces address %s", addr.String())
	}
}

// TestListenWithFallback_ExplicitLoopbackHonoured proves that passing
// an explicit loopback host still works (no over-correction in the
// empty-host default). Tests both the literal IP and the conventional
// hostname.
func TestListenWithFallback_ExplicitLoopbackHonoured(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost"} {
		t.Run(host, func(t *testing.T) {
			ln, err := ListenWithFallback(host, 0, 0)
			if err != nil {
				t.Fatalf("ListenWithFallback(%q): %v", host, err)
			}
			defer ln.Close()
			if !ln.Addr().(*net.TCPAddr).IP.IsLoopback() {
				t.Errorf("host=%q bound to non-loopback %s", host, ln.Addr())
			}
		})
	}
}
