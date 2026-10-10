//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// backupDumpstoreConf copies dumpstore.conf aside and restores it
// byte-for-byte when the test ends, so config-writing endpoints (TLS,
// password) cannot leave the VM configured differently for the next deploy.
// The running process keeps its in-memory copy until restarted; the file is
// what the next start reads.
func backupDumpstoreConf(t *testing.T) {
	t.Helper()
	const backup = "/tmp/itest-dumpstore.conf"
	conf := "$(ls /etc/dumpstore/dumpstore.conf /usr/local/etc/dumpstore/dumpstore.conf 2>/dev/null | head -1)"
	vmExec(t, "c="+conf+"; [ -n \"$c\" ] && cp -p \"$c\" "+backup)
	t.Cleanup(func() {
		if out, err := vmExecErr("c=" + conf + "; mv " + backup + " \"$c\""); err != nil {
			t.Errorf("restoring dumpstore.conf failed — fix the VM by hand: %v\n%s", err, out)
		}
	})
}

// TestTLSGencert generates a self-signed certificate and points the TLS
// config at it. dumpstore only serves TLS when started with --tls, so the
// running suite keeps talking plain HTTP either way.
func TestTLSGencert(t *testing.T) {
	const certDir = "/etc/dumpstore/itest-tls"
	const host = "itest.dumpstore.local"
	backupDumpstoreConf(t)
	_, _ = vmExecErr("rm -rf " + certDir)
	t.Cleanup(func() { _, _ = vmExecErr("rm -rf " + certDir) })

	apiStatus(t, http.StatusBadRequest, "POST", "/api/tls/gencert", map[string]string{"hostname": "bad host!"})
	apiStatus(t, http.StatusBadRequest, "POST", "/api/tls/gencert", map[string]string{"hostname": host, "cert_dir": "relative/dir"})

	assertTasks(t, apiOK(t, "POST", "/api/tls/gencert", map[string]string{"hostname": host, "cert_dir": certDir}))
	st := decode[struct {
		Enabled    bool   `json:"enabled"`
		CN         string `json:"cn"`
		SelfSigned bool   `json:"self_signed"`
		CertPath   string `json:"cert_path"`
	}](t, apiOK(t, "GET", "/api/tls/status", nil))
	if !st.Enabled || st.CN != host || !st.SelfSigned || st.CertPath != certDir+"/cert.pem" {
		t.Fatalf("tls/status after gencert: %+v", st)
	}
	vmExec(t, "test -s "+certDir+"/cert.pem && test -s "+certDir+"/key.pem")

	assertTasks(t, apiOK(t, "PATCH", "/api/tls/config", map[string]string{
		"cert_path": certDir + "/cert.pem", "key_path": certDir + "/key.pem",
	}))
	// A pair that does not load is refused before anything is written.
	apiStatus(t, http.StatusBadRequest, "PATCH", "/api/tls/config", map[string]string{
		"cert_path": certDir + "/missing.pem", "key_path": certDir + "/key.pem",
	})
	apiStatus(t, http.StatusBadRequest, "PATCH", "/api/tls/config", map[string]string{"cert_path": certDir + "/cert.pem"})
}
