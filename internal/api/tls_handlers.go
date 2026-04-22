package api

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var (
	reDomain  = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`)
	reEmail   = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	reTLSPath = regexp.MustCompile(`^/[^\x00;|&$` + "`" + `]+$`)
)

type tlsStatusResponse struct {
	Enabled       bool     `json:"enabled"`
	CertPath      string   `json:"cert_path,omitempty"`
	KeyPath       string   `json:"key_path,omitempty"`
	CN            string   `json:"cn,omitempty"`
	SANs          []string `json:"sans,omitempty"`
	ExpiresAt     string   `json:"expires_at,omitempty"`
	DaysRemaining int      `json:"days_remaining,omitempty"`
	SelfSigned    bool     `json:"self_signed,omitempty"`
	ACMEEmail     string   `json:"acme_email,omitempty"`
	ACMEDomain    string   `json:"acme_domain,omitempty"`
}

// getTLSStatus returns the current TLS configuration and cert metadata.
func (h *Handler) getTLSStatus(w http.ResponseWriter, r *http.Request) {
	h.authMu.RLock()
	resp := tlsStatusResponse{
		Enabled:    h.authCfg.TLSEnabled,
		CertPath:   h.authCfg.TLSCertPath,
		KeyPath:    h.authCfg.TLSKeyPath,
		ACMEEmail:  h.authCfg.ACMEEmail,
		ACMEDomain: h.authCfg.ACMEDomain,
	}
	certPath := h.authCfg.TLSCertPath
	h.authMu.RUnlock()

	if certPath != "" {
		if meta, err := parseCertMeta(certPath); err == nil {
			resp.CN = meta.cn
			resp.SANs = meta.sans
			resp.ExpiresAt = meta.expiresAt.Format(time.RFC3339)
			resp.DaysRemaining = int(time.Until(meta.expiresAt).Hours() / 24)
			resp.SelfSigned = meta.selfSigned
		}
	}

	writeJSON(r.Context(), w, resp)
}

// tlsGenCert generates a self-signed certificate via the tls_gencert.yml playbook.
func (h *Handler) tlsGenCert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hostname string `json:"hostname"`
		CertDir  string `json:"cert_dir"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid request body"), nil)
		return
	}
	if req.Hostname == "" {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("hostname is required"), nil)
		return
	}
	if !reDomain.MatchString(req.Hostname) {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid hostname"), nil)
		return
	}
	if req.CertDir == "" {
		req.CertDir = filepath.Dir(h.configPath) + "/tls"
	}
	if !reTLSPath.MatchString(req.CertDir) {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid cert_dir"), nil)
		return
	}

	out, err := h.runOp("tls_gencert.yml", map[string]string{
		"hostname": req.Hostname,
		"cert_dir": req.CertDir,
	})
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}

	certPath := req.CertDir + "/cert.pem"
	keyPath := req.CertDir + "/key.pem"

	cfgOut, cfgErr := h.runOp("tls_set_config.yml", map[string]string{
		"config_path": h.configPath,
		"cert_path":   certPath,
		"key_path":    keyPath,
		"enabled":     "true",
	})
	if cfgErr != nil {
		steps := out.Steps()
		if cfgOut != nil {
			steps = append(steps, cfgOut.Steps()...)
		}
		writeError(r.Context(), w, http.StatusInternalServerError, cfgErr, steps)
		return
	}

	h.authMu.Lock()
	h.authCfg.TLSCertPath = certPath
	h.authCfg.TLSKeyPath = keyPath
	h.authCfg.TLSEnabled = true
	h.authMu.Unlock()

	auditLog(r.Context(), r, "tls.gencert", req.Hostname, nil)

	steps := out.Steps()
	steps = append(steps, cfgOut.Steps()...)
	writeJSON(r.Context(), w, map[string]any{"tasks": steps})
}

// tlsSetConfig validates and persists an existing cert+key pair.
func (h *Handler) tlsSetConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CertPath string `json:"cert_path"`
		KeyPath  string `json:"key_path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid request body"), nil)
		return
	}
	if req.CertPath == "" || req.KeyPath == "" {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("cert_path and key_path are required"), nil)
		return
	}
	if !reTLSPath.MatchString(req.CertPath) || !reTLSPath.MatchString(req.KeyPath) {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid path"), nil)
		return
	}

	// Validate the cert+key pair before persisting.
	if _, err := tls.LoadX509KeyPair(req.CertPath, req.KeyPath); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid cert/key pair: %w", err), nil)
		return
	}

	out, err := h.runOp("tls_set_config.yml", map[string]string{
		"config_path": h.configPath,
		"cert_path":   req.CertPath,
		"key_path":    req.KeyPath,
		"enabled":     "true",
	})
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}

	h.authMu.Lock()
	h.authCfg.TLSCertPath = req.CertPath
	h.authCfg.TLSKeyPath = req.KeyPath
	h.authCfg.TLSEnabled = true
	h.authMu.Unlock()

	auditLog(r.Context(), r, "tls.set_config", req.CertPath, nil)
	writeJSON(r.Context(), w, map[string]any{"tasks": out.Steps()})
}

// tlsAcmeIssue issues a certificate via ACME using lego.
func (h *Handler) tlsAcmeIssue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email   string `json:"email"`
		Domain  string `json:"domain"`
		CertDir string `json:"cert_dir"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid request body"), nil)
		return
	}
	if req.Email == "" || req.Domain == "" {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("email and domain are required"), nil)
		return
	}
	if !reEmail.MatchString(req.Email) {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid email address"), nil)
		return
	}
	if !reDomain.MatchString(req.Domain) {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid domain name"), nil)
		return
	}
	if req.CertDir == "" {
		req.CertDir = filepath.Dir(h.configPath) + "/tls/acme"
	}
	if !reTLSPath.MatchString(req.CertDir) {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid cert_dir"), nil)
		return
	}

	out, err := h.runOp("tls_acme_issue.yml", map[string]string{
		"email":    req.Email,
		"domain":   req.Domain,
		"cert_dir": req.CertDir,
	})
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}

	// lego stores certs at <cert_dir>/certificates/<domain>.crt and .key
	certPath := req.CertDir + "/certificates/" + req.Domain + ".crt"
	keyPath := req.CertDir + "/certificates/" + req.Domain + ".key"

	cfgOut, cfgErr := h.runOp("tls_set_config.yml", map[string]string{
		"config_path": h.configPath,
		"cert_path":   certPath,
		"key_path":    keyPath,
		"enabled":     "true",
	})
	if cfgErr != nil {
		steps := out.Steps()
		if cfgOut != nil {
			steps = append(steps, cfgOut.Steps()...)
		}
		writeError(r.Context(), w, http.StatusInternalServerError, cfgErr, steps)
		return
	}

	h.authMu.Lock()
	h.authCfg.TLSCertPath = certPath
	h.authCfg.TLSKeyPath = keyPath
	h.authCfg.TLSEnabled = true
	h.authCfg.ACMEEnabled = true
	h.authCfg.ACMEEmail = req.Email
	h.authCfg.ACMEDomain = req.Domain
	h.authMu.Unlock()

	auditLog(r.Context(), r, "tls.acme_issue", req.Domain, nil)

	steps := out.Steps()
	steps = append(steps, cfgOut.Steps()...)
	writeJSON(r.Context(), w, map[string]any{"tasks": steps})
}

// tlsAcmeRenew renews the ACME certificate using stored config.
func (h *Handler) tlsAcmeRenew(w http.ResponseWriter, r *http.Request) {
	h.authMu.RLock()
	acmeEnabled := h.authCfg.ACMEEnabled
	acmeEmail := h.authCfg.ACMEEmail
	acmeDomain := h.authCfg.ACMEDomain
	certPath := h.authCfg.TLSCertPath
	h.authMu.RUnlock()

	if !acmeEnabled {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("ACME is not configured"), nil)
		return
	}
	if acmeEmail == "" || acmeDomain == "" {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("ACME email and domain are not set"), nil)
		return
	}

	// Derive cert_dir from stored cert path (parent of certificates/<domain>.crt)
	certDir := filepath.Dir(h.configPath) + "/tls/acme"
	if certPath != "" {
		// Walk up two levels from <cert_dir>/certificates/<domain>.crt
		certDir = certDirFromCertPath(certPath, h.configPath)
	}

	out, err := h.runOp("tls_acme_renew.yml", map[string]string{
		"email":    acmeEmail,
		"domain":   acmeDomain,
		"cert_dir": certDir,
	})
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}

	auditLog(r.Context(), r, "tls.acme_renew", acmeDomain, nil)
	writeJSON(r.Context(), w, map[string]any{"tasks": out.Steps()})
}

// certDirFromCertPath derives the lego root dir from a cert path of the form
// <certDir>/certificates/<domain>.crt
func certDirFromCertPath(certPath, configPath string) string {
	// Strip /certificates/<domain>.crt — go up two path segments
	for i := len(certPath) - 1; i >= 0; i-- {
		if certPath[i] == '/' {
			certPath = certPath[:i]
			break
		}
	}
	for i := len(certPath) - 1; i >= 0; i-- {
		if certPath[i] == '/' {
			return certPath[:i]
		}
	}
	return filepath.Dir(configPath) + "/tls/acme"
}

type certMeta struct {
	cn         string
	sans       []string
	expiresAt  time.Time
	selfSigned bool
}

func parseCertMeta(certPath string) (*certMeta, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	var sans []string
	for _, name := range cert.DNSNames {
		sans = append(sans, name)
	}
	for _, ip := range cert.IPAddresses {
		sans = append(sans, ip.String())
	}

	selfSigned := cert.Issuer.String() == cert.Subject.String()

	return &certMeta{
		cn:         cert.Subject.CommonName,
		sans:       sans,
		expiresAt:  cert.NotAfter,
		selfSigned: selfSigned,
	}, nil
}
