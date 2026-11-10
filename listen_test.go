package main

import "testing"

// TestTLSAddrs: without a password the HTTPS and redirect listeners must
// stay on loopback like the plain listener (#192); with one they bind all
// interfaces as before.
func TestTLSAddrs(t *testing.T) {
	for _, c := range []struct {
		loopbackOnly            bool
		wantHTTPS, wantRedirect string
	}{
		{true, "127.0.0.1:443", "127.0.0.1:80"},
		{false, ":443", ":80"},
	} {
		https, redirect := tlsAddrs(c.loopbackOnly, "443", "80")
		if https != c.wantHTTPS || redirect != c.wantRedirect {
			t.Errorf("tlsAddrs(loopbackOnly=%v) = %q, %q; want %q, %q",
				c.loopbackOnly, https, redirect, c.wantHTTPS, c.wantRedirect)
		}
	}
}
