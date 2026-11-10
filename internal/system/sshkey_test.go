package system

import "testing"

// Expected fingerprints are from `ssh-keygen -lf` on the same public keys.
func TestParseAuthorizedKey(t *testing.T) {
	const ed = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDSHozuGRA7B8FAbzccAJsoBiXDfL8jIAAu7SNILOB2a"
	const ec = "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBOtuMhEG5LWhXkoeUBCzktJioLTcvmg5phRtDehyu9EKninOX6pGfbH4gPhKPd0P7Q+Vv1ZjCqA7BbUVr91DOX8="
	cases := []struct {
		line, typ, fp, comment string
	}{
		{ed + " alice@laptop", "ssh-ed25519", "SHA256:a1ZSPmADOkmNRQozRPfkpT+3L/FxfP+/OF+7UHWjENQ", "alice@laptop"},
		{ec, "ecdsa-sha2-nistp256", "SHA256:gZxKJz8cx5a9W7ko6hmlJHceryuOpTwrxxBU8o0W4Ho", ""},
		{`from="10.0.0.0/8",no-pty ` + ed + " with spaces in comment", "ssh-ed25519", "SHA256:a1ZSPmADOkmNRQozRPfkpT+3L/FxfP+/OF+7UHWjENQ", "with spaces in comment"},
		{"not a key at all", "", "", ""},
		{"ssh-ed25519 !!notbase64!!", "", "", ""},
	}
	for _, c := range cases {
		k := ParseAuthorizedKey(c.line)
		if k.Key != c.line || k.Type != c.typ || k.Fingerprint != c.fp || k.Comment != c.comment {
			t.Errorf("ParseAuthorizedKey(%q) = %+v; want type=%q fp=%q comment=%q", c.line, k, c.typ, c.fp, c.comment)
		}
	}
}
