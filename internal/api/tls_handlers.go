package api

import (
	"context"
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
func (h *Handler) getTLSStatus(context.Context, struct{}) (tlsStatusResponse, error) {
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
	return resp, nil
}

// tlsGenCert generates a self-signed certificate via the tls_gencert.yml playbook.
func (h *Handler) tlsGenCert(ctx context.Context, in struct {
	Body GenCertRequest `body:""`
}) (TaskResult, error) {
	req := in.Body
	if req.Hostname == "" {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("hostname is required"))
	}
	if !reDomain.MatchString(req.Hostname) {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("invalid hostname"))
	}
	if req.CertDir == "" {
		req.CertDir = filepath.Dir(h.configPath) + "/tls"
	}
	if !reTLSPath.MatchString(req.CertDir) {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("invalid cert_dir"))
	}

	out, err := h.runOp(ctx, "tls_gencert.yml", map[string]string{
		"hostname": req.Hostname,
		"cert_dir": req.CertDir,
	})
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}

	certPath := req.CertDir + "/cert.pem"
	keyPath := req.CertDir + "/key.pem"

	cfgOut, cfgErr := h.runOp(ctx, "tls_set_config.yml", map[string]string{
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
		return TaskResult{}, opError(cfgErr, steps)
	}

	h.authMu.Lock()
	h.authCfg.TLSCertPath = certPath
	h.authCfg.TLSKeyPath = keyPath
	h.authCfg.TLSEnabled = true
	h.authMu.Unlock()

	auditLog(ctx, "tls.gencert", req.Hostname, nil)

	steps := out.Steps()
	steps = append(steps, cfgOut.Steps()...)
	return TaskResult{Tasks: steps}, nil
}

// GenCertRequest is the POST /api/tls/gencert body.
type GenCertRequest struct {
	Hostname string `json:"hostname"`
	CertDir  string `json:"cert_dir,omitempty" doc:"Default: <config dir>/tls"`
}

// tlsSetConfig validates and persists an existing cert+key pair.
func (h *Handler) tlsSetConfig(ctx context.Context, in struct {
	Body TLSConfigRequest `body:""`
}) (TaskResult, error) {
	req := in.Body
	if req.CertPath == "" || req.KeyPath == "" {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("cert_path and key_path are required"))
	}
	if !reTLSPath.MatchString(req.CertPath) || !reTLSPath.MatchString(req.KeyPath) {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("invalid path"))
	}

	// Validate the cert+key pair before persisting.
	if _, err := tls.LoadX509KeyPair(req.CertPath, req.KeyPath); err != nil {
		return TaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid cert/key pair: %w", err))
	}

	out, err := h.runOp(ctx, "tls_set_config.yml", map[string]string{
		"config_path": h.configPath,
		"cert_path":   req.CertPath,
		"key_path":    req.KeyPath,
		"enabled":     "true",
	})
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}

	h.authMu.Lock()
	h.authCfg.TLSCertPath = req.CertPath
	h.authCfg.TLSKeyPath = req.KeyPath
	h.authCfg.TLSEnabled = true
	h.authMu.Unlock()

	auditLog(ctx, "tls.set_config", req.CertPath, nil)
	return TaskResult{Tasks: out.Steps()}, nil
}

// TLSConfigRequest is the PATCH /api/tls/config body.
type TLSConfigRequest struct {
	CertPath string `json:"cert_path"`
	KeyPath  string `json:"key_path"`
}

// tlsAcmeIssue issues a certificate via ACME using lego.
func (h *Handler) tlsAcmeIssue(ctx context.Context, in struct {
	Body ACMEIssueRequest `body:""`
}) (TaskResult, error) {
	req := in.Body
	if req.Email == "" || req.Domain == "" {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("email and domain are required"))
	}
	if !reEmail.MatchString(req.Email) {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("invalid email address"))
	}
	if !reDomain.MatchString(req.Domain) {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("invalid domain name"))
	}
	if req.CertDir == "" {
		req.CertDir = filepath.Dir(h.configPath) + "/tls/acme"
	}
	if !reTLSPath.MatchString(req.CertDir) {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("invalid cert_dir"))
	}

	out, err := h.runOp(ctx, "tls_acme_issue.yml", map[string]string{
		"email":    req.Email,
		"domain":   req.Domain,
		"cert_dir": req.CertDir,
	})
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}

	// lego stores certs at <cert_dir>/certificates/<domain>.crt and .key
	certPath := req.CertDir + "/certificates/" + req.Domain + ".crt"
	keyPath := req.CertDir + "/certificates/" + req.Domain + ".key"

	cfgOut, cfgErr := h.runOp(ctx, "tls_set_config.yml", map[string]string{
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
		return TaskResult{}, opError(cfgErr, steps)
	}

	h.authMu.Lock()
	h.authCfg.TLSCertPath = certPath
	h.authCfg.TLSKeyPath = keyPath
	h.authCfg.TLSEnabled = true
	h.authCfg.ACMEEnabled = true
	h.authCfg.ACMEEmail = req.Email
	h.authCfg.ACMEDomain = req.Domain
	h.authMu.Unlock()

	auditLog(ctx, "tls.acme_issue", req.Domain, nil)

	steps := out.Steps()
	steps = append(steps, cfgOut.Steps()...)
	return TaskResult{Tasks: steps}, nil
}

// ACMEIssueRequest is the POST /api/tls/acme/issue body.
type ACMEIssueRequest struct {
	Email   string `json:"email"`
	Domain  string `json:"domain"`
	CertDir string `json:"cert_dir,omitempty" doc:"Default: <config dir>/tls/acme"`
}

// tlsAcmeRenew renews the ACME certificate using stored config.
func (h *Handler) tlsAcmeRenew(ctx context.Context, _ struct{}) (TaskResult, error) {
	h.authMu.RLock()
	acmeEnabled := h.authCfg.ACMEEnabled
	acmeEmail := h.authCfg.ACMEEmail
	acmeDomain := h.authCfg.ACMEDomain
	certPath := h.authCfg.TLSCertPath
	h.authMu.RUnlock()

	if !acmeEnabled {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("ACME is not configured"))
	}
	if acmeEmail == "" || acmeDomain == "" {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("ACME email and domain are not set"))
	}

	// Derive cert_dir from stored cert path (parent of certificates/<domain>.crt)
	certDir := filepath.Dir(h.configPath) + "/tls/acme"
	if certPath != "" {
		// Walk up two levels from <cert_dir>/certificates/<domain>.crt
		certDir = certDirFromCertPath(certPath, h.configPath)
	}

	out, err := h.runOp(ctx, "tls_acme_renew.yml", map[string]string{
		"email":    acmeEmail,
		"domain":   acmeDomain,
		"cert_dir": certDir,
	})
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}

	auditLog(ctx, "tls.acme_renew", acmeDomain, nil)
	return TaskResult{Tasks: out.Steps()}, nil
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
