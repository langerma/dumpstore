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

// TestRedirectTarget: the HTTP→HTTPS redirect must point at the TLS port.
func TestRedirectTarget(t *testing.T) {
	for _, c := range []struct{ host, tlsPort, want string }{
		{"nas.local:8082", "8443", "https://nas.local:8443/login?x=1"},
		{"nas.local", "8443", "https://nas.local:8443/login?x=1"},
		{"nas.local:80", "443", "https://nas.local/login?x=1"},
		{"nas.local", "443", "https://nas.local/login?x=1"},
		{"192.0.2.1:8080", "8443", "https://192.0.2.1:8443/login?x=1"},
		{"[2001:db8::1]:8080", "8443", "https://[2001:db8::1]:8443/login?x=1"},
		{"[2001:db8::1]:80", "443", "https://[2001:db8::1]/login?x=1"},
		{"[2001:db8::1]", "443", "https://[2001:db8::1]/login?x=1"},
	} {
		if got := redirectTarget(c.host, c.tlsPort, "/login?x=1"); got != c.want {
			t.Errorf("redirectTarget(%q, %q) = %q, want %q", c.host, c.tlsPort, got, c.want)
		}
	}
}
