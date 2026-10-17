package smb

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"text/template"
)

// wsdd (WS-Discovery) makes the Samba host visible in the Windows Network
// view. It announces only the host and its workgroup, so dumpstore treats it
// as part of the Samba config it owns: the workgroup is always taken from
// smb.conf, never configured separately.
//
// On Linux the daemon reads its arguments from the unit's EnvironmentFile
// (WSDD_PARAMS), which dumpstore renders. On FreeBSD the rc.d script reads
// the workgroup from smb4.conf itself, so there is no file to render.

//go:embed wsdd.default.tmpl
var wsddTemplate string

// WSDDMarker is the header line identifying a dumpstore-rendered wsdd file.
const WSDDMarker = "Managed by dumpstore"

// WSDDParamsVar is the variable the wsdd unit expands into its command line.
const WSDDParamsVar = "WSDD_PARAMS"

// wsddWorkgroup matches workgroup names safe to pass as one unquoted word —
// systemd splits $WSDD_PARAMS on whitespace. NetBIOS caps names at 15 chars.
var wsddWorkgroup = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$`)

// WSDDWorkgroup returns the workgroup to announce, or "" when the smb.conf
// value cannot be passed safely (wsdd then falls back to WORKGROUP).
func (c *SMBConfig) WSDDWorkgroup() string {
	if wsddWorkgroup.MatchString(c.Global.Workgroup) {
		return c.Global.Workgroup
	}
	return ""
}

// RenderWSDDToFile renders the wsdd options file for this config to dst.
func (c *SMBConfig) RenderWSDDToFile(dst string) error {
	tmpl, err := template.New("wsdd").Funcs(template.FuncMap{
		"marker":    func() string { return WSDDMarker },
		"workgroup": c.WSDDWorkgroup,
	}).Parse(wsddTemplate)
	if err != nil {
		return fmt.Errorf("parse wsdd template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		return fmt.Errorf("render wsdd config: %w", err)
	}
	if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write wsdd config: %w", err)
	}
	return nil
}

// WSDDManaged reports whether path holds a dumpstore-rendered wsdd file.
func WSDDManaged(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && bytes.Contains(data, []byte(WSDDMarker))
}
