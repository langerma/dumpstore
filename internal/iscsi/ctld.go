package iscsi

import (
	"bytes"
	_ "embed"
	"fmt"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// FreeBSD / ctld (#186).
//
// dumpstore fully owns /etc/ctl.conf: every write parses the file into a
// []Target, changes the slice, and renders the whole file again from
// ctl.conf.tmpl. The file itself is the source of truth — no side state.

const (
	// CtlConfPath is the ctld configuration file dumpstore owns.
	CtlConfPath = "/etc/ctl.conf"
	// CtlConfBackupPath holds the file as it was before dumpstore took over.
	CtlConfBackupPath = CtlConfPath + ".pre-dumpstore"

	// ctlHeader starts every rendered file; its presence marks the file as
	// dumpstore-managed (the init gate).
	ctlHeader = "# Managed by dumpstore"
)

//go:embed ctl.conf.tmpl
var ctlConfTmpl string

var ctlTmpl = template.Must(template.New("ctl.conf").Parse(ctlConfTmpl))

// CtldInitialized reports whether /etc/ctl.conf exists and was rendered by
// dumpstore.
func CtldInitialized() bool {
	data, err := os.ReadFile(CtlConfPath)
	return err == nil && isManaged(data)
}

func isManaged(data []byte) bool { return bytes.HasPrefix(data, []byte(ctlHeader)) }

func listFreeBSDTargets() ([]Target, error) { return LoadCtld() }

// LoadCtld reads /etc/ctl.conf and returns its zvol-backed targets,
// including CHAP credentials. A missing file yields an empty slice.
func LoadCtld() ([]Target, error) {
	data, err := os.ReadFile(CtlConfPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Target{}, nil
		}
		return nil, fmt.Errorf("reading ctl.conf: %w", err)
	}
	return ParseCtld(string(data)), nil
}

// ReadCtlConfRaw returns the current /etc/ctl.conf content ("" if missing).
func ReadCtlConfRaw() (string, error) {
	data, err := os.ReadFile(CtlConfPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading ctl.conf: %w", err)
	}
	return string(data), nil
}

// ParseCtld extracts the zvol-backed targets from ctl.conf content.
func ParseCtld(content string) []Target {
	targets := []Target{}
	for _, t := range parseCtl(content).targets {
		if t.ZvolName != "" {
			targets = append(targets, t.Target)
		}
	}
	return targets
}

// TargetNames returns the name of every target block in ctl.conf content,
// zvol-backed or not.
func TargetNames(content string) []string {
	var names []string
	for _, t := range parseCtl(content).targets {
		names = append(names, t.IQN)
	}
	return names
}

// Import returns the targets in content that dumpstore's model represents
// exactly — zvol-backed, LUN 0 only, a single portal, auth none or one CHAP
// credential — and a description of everything else, which is left out of
// the rendered file (it stays in CtlConfBackupPath). Used once by init to
// take over an existing ctl.conf, including the old block-patched format.
func Import(content string) (targets []Target, skipped []string) {
	parsed := parseCtl(content)
	seenIQN := map[string]bool{}
	seenZvol := map[string]bool{}
	targets = []Target{}
	for _, t := range parsed.targets {
		issues := t.issues
		if len(issues) == 0 {
			switch {
			case seenIQN[strings.ToLower(t.IQN)]:
				issues = []string{"duplicate target name"}
			case seenZvol[t.ZvolName]:
				issues = []string{"zvol " + t.ZvolName + " is already exported by another target"}
			}
		}
		if len(issues) > 0 {
			skipped = append(skipped, "target "+t.IQN+" ("+strings.Join(issues, "; ")+")")
			continue
		}
		seenIQN[strings.ToLower(t.IQN)] = true
		seenZvol[t.ZvolName] = true
		t.Portals = []string{t.portal}
		targets = append(targets, t.Target)
	}
	skipped = append(skipped, parsed.other...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].IQN < targets[j].IQN })
	return targets, skipped
}

// ── Rendering ────────────────────────────────────────────────────────────────

var (
	reCtlName = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
	reCtlPath = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)
)

type ctlPortalGroup struct {
	Name   string
	Listen string
}

type ctlRenderTarget struct {
	IQN         string
	PortalGroup string
	AuthMode    string
	CHAPUser    string
	CHAPSecret  string
	Initiators  []string
	Device      string
}

// portalGroupName derives a stable portal-group name from a canonical listen
// address, so adding or removing other addresses never renames (and so never
// recreates) an existing group's ports. ctld's lexer accepts '.', ':', '[' and
// ']' in bare names — the same token class as the listen address itself.
func portalGroupName(listen string) string { return "dumpstore-" + listen }

// Render returns the complete ctl.conf for targets: one portal group per
// distinct listen address, discovery open and filtered by what each
// initiator may reach, and per-target auth. Output is deterministic.
// Targets are validated here because ctl.conf strings have no escapes.
func Render(targets []Target) ([]byte, error) {
	pgByListen := map[string]string{}
	seenIQN := map[string]bool{}
	seenZvol := map[string]bool{}
	var rts []ctlRenderTarget

	for _, t := range targets {
		if !reCtlName.MatchString(t.IQN) {
			return nil, fmt.Errorf("invalid target name %q", t.IQN)
		}
		if seenIQN[strings.ToLower(t.IQN)] {
			return nil, fmt.Errorf("duplicate target %s", t.IQN)
		}
		seenIQN[strings.ToLower(t.IQN)] = true
		if t.ZvolName == "" || !reCtlPath.MatchString(t.ZvolName) {
			return nil, fmt.Errorf("target %s: invalid zvol %q", t.IQN, t.ZvolName)
		}
		if seenZvol[t.ZvolName] {
			return nil, fmt.Errorf("zvol %s is exported by more than one target", t.ZvolName)
		}
		seenZvol[t.ZvolName] = true
		if len(t.Portals) != 1 {
			return nil, fmt.Errorf("target %s: exactly one portal required, have %d", t.IQN, len(t.Portals))
		}
		listen, ok := normalizePortal(t.Portals[0])
		if !ok {
			return nil, fmt.Errorf("target %s: invalid portal %q", t.IQN, t.Portals[0])
		}
		for _, ini := range t.Initiators {
			if !reCtlName.MatchString(ini) {
				return nil, fmt.Errorf("target %s: invalid initiator name %q", t.IQN, ini)
			}
		}
		switch t.AuthMode {
		case "none":
		case "chap":
			if !validCHAPString(t.CHAPUser) || !validCHAPString(t.CHAPSecret) {
				return nil, fmt.Errorf("target %s: CHAP user and secret must be non-empty and must not contain quotes, backslashes or newlines", t.IQN)
			}
		default:
			return nil, fmt.Errorf("target %s: unsupported auth mode %q", t.IQN, t.AuthMode)
		}

		pg := portalGroupName(listen)
		pgByListen[listen] = pg
		rt := ctlRenderTarget{
			IQN:         t.IQN,
			PortalGroup: pg,
			AuthMode:    t.AuthMode,
			Initiators:  t.Initiators,
			Device:      "/dev/zvol/" + t.ZvolName,
		}
		if t.AuthMode == "chap" {
			rt.CHAPUser, rt.CHAPSecret = t.CHAPUser, t.CHAPSecret
		}
		rts = append(rts, rt)
	}

	pgs := make([]ctlPortalGroup, 0, len(pgByListen))
	for listen, name := range pgByListen {
		pgs = append(pgs, ctlPortalGroup{Name: name, Listen: listen})
	}
	sort.Slice(pgs, func(i, j int) bool { return pgs[i].Listen < pgs[j].Listen })
	sort.Slice(rts, func(i, j int) bool { return rts[i].IQN < rts[j].IQN })

	var buf bytes.Buffer
	err := ctlTmpl.Execute(&buf, map[string]any{
		"Header":       ctlHeader,
		"Backup":       CtlConfBackupPath,
		"PortalGroups": pgs,
		"Targets":      rts,
	})
	if err != nil {
		return nil, fmt.Errorf("rendering ctl.conf: %w", err)
	}
	return buf.Bytes(), nil
}

// Listeners returns the sockets ctld must hold for targets' portals, in
// sockstat(1) form ("tcp4 *:3260", "tcp6 ::1:3260"), sorted and unique.
// ctld creates a target's kernel port even when binding its portal fails,
// so the port list alone cannot prove a reload worked; the listeners can.
func Listeners(targets []Target) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range targets {
		for _, p := range t.Portals {
			n, ok := normalizePortal(p)
			if !ok {
				continue
			}
			host, port, _ := net.SplitHostPort(n)
			ip := net.ParseIP(host)
			proto, addr := "tcp6", ip.String()
			if ip.To4() != nil {
				proto = "tcp4"
			}
			if ip.IsUnspecified() {
				addr = "*"
			}
			key := proto + " " + addr + ":" + port
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	sort.Strings(out)
	return out
}

// validCHAPString reports whether s can be written as a quoted ctl.conf
// string, which has no escape sequences.
func validCHAPString(s string) bool {
	return s != "" && !strings.ContainsAny(s, "\"\\\n\r")
}

// normalizePortal returns the canonical "host:port" (IPv6 bracketed) for a
// listen address. It accepts a missing port (3260) and the unbracketed
// IPv6 form ":::3260" that the old block-patching playbook wrote.
func normalizePortal(s string) (string, bool) {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		if net.ParseIP(s) != nil {
			host, port = s, "3260"
		} else if i := strings.LastIndex(s, ":"); i > 0 && net.ParseIP(s[:i]) != nil {
			host, port = s[:i], s[i+1:]
		} else {
			return "", false
		}
	}
	ip := net.ParseIP(host)
	n, err := strconv.Atoi(port)
	if ip == nil || err != nil || n < 1 || n > 65535 {
		return "", false
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(n)), true
}

// ── Parsing ──────────────────────────────────────────────────────────────────

// ctlTarget is a parsed target block plus everything that keeps Import from
// representing it faithfully.
type ctlTarget struct {
	Target
	portal string // normalised single portal, set when importable
	issues []string
}

type ctlParsed struct {
	targets []ctlTarget
	other   []string // top-level settings and blocks that are not targets or portal groups
}

type ctlPG struct {
	listens []string
	extras  []string // settings beyond listen/discovery-*
}

// parseCtl parses ctl.conf content in two passes: portal groups first (a
// target may reference a group defined after it), then everything else.
func parseCtl(content string) ctlParsed {
	lines := strings.Split(content, "\n")

	pgs := map[string]*ctlPG{}
	for i := 0; i < len(lines); i++ {
		f := ctlFields(lines[i])
		if len(f) >= 3 && f[0] == "portal-group" && f[len(f)-1] == "{" {
			block, end := extractBlock(lines, i)
			pg := &ctlPG{}
			if prev, ok := pgs[f[1]]; ok {
				pg = prev
			}
			pgs[f[1]] = pg
			for _, bl := range block {
				bf := ctlFields(bl)
				if len(bf) == 0 {
					continue
				}
				switch bf[0] {
				case "listen":
					if len(bf) >= 2 {
						pg.listens = append(pg.listens, bf[1])
					}
				case "discovery-auth-group", "discovery-filter":
				default:
					pg.extras = append(pg.extras, bf[0])
				}
			}
			i = end
		}
	}

	var p ctlParsed
	for i := 0; i < len(lines); i++ {
		f := ctlFields(lines[i])
		if len(f) == 0 {
			continue
		}
		isBlock := f[len(f)-1] == "{"
		switch {
		case isBlock && f[0] == "portal-group":
			_, i = extractBlock(lines, i)
		case isBlock && f[0] == "target" && len(f) >= 3:
			var block []string
			block, i = extractBlock(lines, i)
			p.targets = append(p.targets, parseTargetBlock(f[1], block, pgs))
		case isBlock:
			_, i = extractBlock(lines, i)
			p.other = append(p.other, strings.Join(f[:len(f)-1], " ")+" { … }")
		default:
			p.other = append(p.other, "global setting: "+strings.Join(f, " "))
		}
	}
	return p
}

// parseTargetBlock extracts fields from the lines inside a target { } block.
func parseTargetBlock(iqn string, lines []string, pgs map[string]*ctlPG) ctlTarget {
	t := ctlTarget{Target: Target{IQN: iqn, AuthMode: "none", Initiators: []string{}}}
	issue := func(format string, a ...any) { t.issues = append(t.issues, fmt.Sprintf(format, a...)) }
	luns, pgRefs := 0, 0
	authNone := false

	for i := 0; i < len(lines); i++ {
		f := ctlFields(lines[i])
		if len(f) == 0 {
			continue
		}
		if f[len(f)-1] == "{" {
			var block []string
			block, i = extractBlock(lines, i)
			if f[0] != "lun" || len(f) != 3 {
				issue("unsupported block %q", strings.Join(f[:len(f)-1], " "))
				continue
			}
			luns++
			if f[1] != "0" {
				issue("LUN %s (only LUN 0 is managed)", f[1])
			}
			t.LUN, _ = strconv.Atoi(f[1])
			for _, bl := range block {
				bf := ctlFields(bl)
				switch {
				case len(bf) == 0:
				case bf[0] == "path" && len(bf) == 2:
					t.ZvolDevice = bf[1]
					if strings.HasPrefix(bf[1], "/dev/zvol/") {
						t.ZvolName = strings.TrimPrefix(bf[1], "/dev/zvol/")
					}
				case bf[0] == "backend" && len(bf) == 2 && bf[1] == "block":
				default:
					issue("LUN setting %q", bf[0])
				}
			}
			continue
		}

		switch f[0] {
		case "portal-group":
			pgRefs++
			if len(f) < 2 {
				continue
			}
			if len(f) > 2 {
				issue("per-portal auth-group")
			}
			pg, ok := pgs[f[1]]
			if !ok {
				continue // predefined "default" or undefined
			}
			t.Portals = append(t.Portals, pg.listens...)
			if len(pg.extras) > 0 {
				issue("portal-group %s settings %s", f[1], strings.Join(pg.extras, ", "))
			}
		case "chap":
			if len(f) != 3 || t.CHAPUser != "" {
				issue("multiple or malformed chap credentials")
				continue
			}
			t.AuthMode, t.CHAPUser, t.CHAPSecret = "chap", f[1], f[2]
		case "initiator-name":
			if len(f) == 2 {
				t.Initiators = append(t.Initiators, f[1])
			}
		case "auth-type":
			switch {
			case len(f) == 2 && f[1] == "none":
				authNone = true
			case len(f) == 2 && f[1] == "chap":
			default:
				issue("auth-type %s", strings.Join(f[1:], " "))
			}
		case "auth-group":
			if len(f) == 2 && f[1] == "no-authentication" {
				authNone = true
			} else {
				issue("auth-group %s", strings.Join(f[1:], " "))
			}
		case "lun":
			luns++
			issue("LUN references a named lun block")
		default:
			issue("setting %q", f[0])
		}
	}

	if pgRefs == 0 && len(t.Portals) == 0 {
		// Predefined "default" portal group: listing shows the IPv4 side.
		t.Portals = []string{"0.0.0.0:3260"}
		issue("uses the predefined default portal group (all IPv4 and IPv6 addresses)")
	} else if len(t.Portals) == 0 {
		t.Portals = []string{"0.0.0.0:3260"}
		issue("uses an undefined or predefined portal group")
	} else if len(t.Portals) > 1 {
		issue("%d portals", len(t.Portals))
	} else if p, ok := normalizePortal(t.Portals[0]); ok {
		t.portal = p
	} else {
		issue("unparseable portal %q", t.Portals[0])
	}

	switch {
	case luns == 0:
		issue("no LUN")
	case luns > 1:
		issue("%d LUNs", luns)
	}
	if t.ZvolName == "" && luns > 0 {
		issue("LUN is not a ZFS volume")
	}
	if t.AuthMode == "chap" && authNone {
		issue("both chap and auth none")
	}
	// A target with no auth clause at all falls back to auth-group "default",
	// which denies all access. It imports as auth none (rendered as
	// no-authentication) — what the old playbook meant by auth_mode none.
	return t
}

// extractBlock returns the lines inside the { } block whose opening line is at
// startIdx, and the index of the closing '}'. Handles nested blocks.
func extractBlock(lines []string, startIdx int) ([]string, int) {
	depth := 0
	var block []string
	for i := startIdx; i < len(lines); i++ {
		l := ctlStripComment(strings.TrimSpace(lines[i]))
		if strings.HasSuffix(l, "{") {
			depth++
			if i == startIdx {
				continue // skip the opening "target ... {" line itself
			}
		}
		if l == "}" {
			depth--
			if depth == 0 {
				return block, i
			}
		}
		if i != startIdx {
			block = append(block, lines[i])
		}
	}
	return block, len(lines) - 1
}

// ctlStripComment removes a trailing # comment from a ctl.conf line. A '#'
// inside a quoted string (e.g. a CHAP secret) is not a comment.
func ctlStripComment(line string) string {
	inQuote := false
	for i, c := range line {
		switch c {
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return strings.TrimSpace(line[:i])
			}
		}
	}
	return strings.TrimSpace(line)
}

// ctlFields splits a ctl.conf line into tokens: comments stripped, quoted
// strings kept whole (quotes removed), ';' treated as a separator.
func ctlFields(line string) []string {
	line = ctlStripComment(line)
	var out []string
	var cur strings.Builder
	inQuote, have := false, false
	for _, c := range line {
		switch {
		case c == '"':
			inQuote = !inQuote
			have = true
		case !inQuote && (c == ' ' || c == '\t' || c == ';'):
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(c)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}
