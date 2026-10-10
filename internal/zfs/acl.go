package zfs

import (
	"fmt"
	"runtime"
	"strings"
)

// ACLEntry represents one access control entry.
// For POSIX ACLs the Tag is "user", "group", "mask", or "other".
// For NFSv4 ACLs the Tag is the ACE type: "A" (allow), "D" (deny), "U" (audit), "L" (alarm).
type ACLEntry struct {
	Tag       string `json:"tag"`
	Flags     string `json:"flags"`     // NFSv4 inheritance flags (e.g. "fd"); empty for POSIX
	Qualifier string `json:"qualifier"` // username/groupname/principal; empty for owner/group/mask/other
	Perms     string `json:"perms"`     // "rwx" (POSIX) or NFSv4 perm chars
	Default   bool   `json:"default"`   // POSIX only: true for default:: entries
}

// DatasetACL holds the full ACL state for a dataset mountpoint.
type DatasetACL struct {
	Dataset    string     `json:"dataset"`
	Mountpoint string     `json:"mountpoint"`
	ACLType    string     `json:"acl_type"` // "posix", "nfsv4", or "off"
	Entries    []ACLEntry `json:"entries"`
}

// GetDatasetACL returns the current ACL for a dataset.
// It reads the acltype and mountpoint ZFS properties, then calls the
// appropriate tool (getfacl or nfs4_getfacl) to get the ACL entries.
func GetDatasetACL(dataset string) (*DatasetACL, error) {
	out, err := run("zfs", "get", "-H", "acltype,mountpoint", dataset)
	if err != nil {
		return nil, fmt.Errorf("zfs get acltype,mountpoint %s: %w", dataset, err)
	}

	acl := &DatasetACL{Dataset: dataset}
	for _, line := range splitLines(out) {
		f := strings.SplitN(line, "\t", 4)
		if len(f) < 3 {
			continue
		}
		// f[0]=dataset f[1]=property f[2]=value
		switch f[1] {
		case "acltype":
			acl.ACLType = normalizeACLType(f[2])
		case "mountpoint":
			acl.Mountpoint = f[2]
		}
	}

	if acl.Mountpoint == "none" || acl.Mountpoint == "-" || acl.Mountpoint == "" || acl.ACLType == "off" {
		return acl, nil
	}

	switch acl.ACLType {
	case "posix":
		acl.Entries, err = getPOSIXACL(acl.Mountpoint)
	case "nfsv4":
		acl.Entries, err = getNFSv4ACL(acl.Mountpoint)
	}
	if err != nil {
		return nil, err
	}
	return acl, nil
}

// DatasetHasACL returns true if the mountpoint has non-trivial POSIX ACL
// entries (more than the base user/group/other entries), or any NFSv4 ACL
// entries. It calls getfacl or nfs4_getfacl directly, so it works regardless
// of whether the ZFS acltype property is set.
//
// An error is returned only when both tools fail (e.g. neither is installed).
// Callers should treat an error as "unknown" and fall back accordingly.
func DatasetHasACL(mountpoint string) (bool, error) {
	// FreeBSD getfacl does not support -c (omit header comments).
	args := []string{"-c", mountpoint}
	if runtime.GOOS == "freebsd" {
		args = []string{mountpoint}
	}
	out, err := run("getfacl", args...)
	if err == nil {
		n := 0
		for _, line := range splitLines(out) {
			if line != "" && !strings.HasPrefix(line, "#") {
				n++
			}
		}
		return n > 3, nil
	}
	posixErr := err

	// Fall back to NFSv4.
	out, err = run("nfs4_getfacl", mountpoint)
	if err == nil {
		for _, line := range splitLines(out) {
			if line != "" && !strings.HasPrefix(line, "#") {
				return true, nil
			}
		}
		return false, nil
	}

	// Both tools failed — likely neither is installed.
	return false, fmt.Errorf("getfacl: %w; nfs4_getfacl: %v", posixErr, err)
}

func normalizeACLType(s string) string {
	switch strings.ToLower(s) {
	case "posix", "posixacl":
		return "posix"
	case "nfsv4", "nfsv4acls":
		return "nfsv4"
	default:
		return "off"
	}
}

// getPOSIXACL runs getfacl and parses the output.
// Uses -c (omit header comments) and -p (absolute paths, no strip of leading /).
func getPOSIXACL(mountpoint string) ([]ACLEntry, error) {
	// FreeBSD getfacl does not support -c (omit header comments).
	args := []string{"-c", "-p", mountpoint}
	if runtime.GOOS == "freebsd" {
		args = []string{"-p", mountpoint}
	}
	out, err := run("getfacl", args...)
	if err != nil {
		return nil, fmt.Errorf("getfacl %s: %w", mountpoint, err)
	}
	var entries []ACLEntry
	for _, line := range splitLines(out) {
		// Skip comment lines that slip through
		if strings.HasPrefix(line, "#") {
			continue
		}
		e, ok := parsePOSIXACLLine(line)
		if ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// parsePOSIXACLLine parses one line from getfacl output.
// Formats:
//   - "user::rwx"             → tag=user qualifier="" perms=rwx default=false
//   - "user:alice:r-x"        → tag=user qualifier=alice perms=r-x default=false
//   - "default:user:alice:rwx" → tag=user qualifier=alice perms=rwx default=true
func parsePOSIXACLLine(line string) (ACLEntry, bool) {
	isDefault := false
	if strings.HasPrefix(line, "default:") {
		isDefault = true
		line = strings.TrimPrefix(line, "default:")
	}

	// Strip effective-rights comment if present (e.g. "user:alice:rwx	#effective:r--")
	if idx := strings.IndexByte(line, '\t'); idx >= 0 {
		line = line[:idx]
	}

	parts := strings.Split(line, ":")
	switch len(parts) {
	case 2:
		// tag::perms (qualifier empty, e.g. "user::rwx" already split into ["user", "", "rwx"] = 3)
		// This case: e.g. "mask:rwx" — tag + perms, no qualifier field
		return ACLEntry{Tag: parts[0], Qualifier: "", Perms: parts[1], Default: isDefault}, true
	case 3:
		return ACLEntry{Tag: parts[0], Qualifier: parts[1], Perms: parts[2], Default: isDefault}, true
	}
	return ACLEntry{}, false
}

// getNFSv4ACL reads the NFSv4 ACL of mountpoint: nfs4_getfacl on Linux,
// base getfacl on FreeBSD (translated to the nfs4-acl-tools entry form so the
// API and UI see one format on both platforms).
func getNFSv4ACL(mountpoint string) ([]ACLEntry, error) {
	if runtime.GOOS == "freebsd" {
		return getFreeBSDNFSv4ACL(mountpoint)
	}
	out, err := run("nfs4_getfacl", mountpoint)
	if err != nil {
		return nil, fmt.Errorf("nfs4_getfacl %s: %w", mountpoint, err)
	}
	var entries []ACLEntry
	for _, line := range splitLines(out) {
		// Skip comment/header lines
		if strings.HasPrefix(line, "#") {
			continue
		}
		e, ok := parseNFSv4ACLLine(line)
		if ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// ParseNFSv4ACE parses an ACE in nfs4-acl-tools form ("A:fd:alice:rwx").
func ParseNFSv4ACE(ace string) (ACLEntry, bool) { return parseNFSv4ACLLine(ace) }

// parseNFSv4ACLLine parses one line from nfs4_getfacl output.
// Format: type:flags:principal:perms
// Example: "A::OWNER@:rwaDxtTnNcCoy"
//
//	"A:fd:GROUP@:rwaDxtTnNcCoy"
//	"D::alice@localdomain:x"
func parseNFSv4ACLLine(line string) (ACLEntry, bool) {
	parts := strings.SplitN(line, ":", 4)
	if len(parts) != 4 {
		return ACLEntry{}, false
	}
	return ACLEntry{
		Tag:       parts[0],
		Flags:     parts[1],
		Qualifier: parts[2],
		Perms:     parts[3],
	}, true
}

// ── FreeBSD NFSv4 ACLs ────────────────────────────────────────────────────────
//
// FreeBSD's base getfacl/setfacl speak NFSv4 ACLs natively, in their own form:
//
//	<tag>[:<qualifier>]:<perms>:<inheritance flags>:<type>
//	owner@:rwxp--aARWcCos:-------:allow
//	user:alice:rwx-----------:fd-----:allow
//
// The API and UI use the nfs4-acl-tools form ("type:flags:principal:perms")
// on every platform; these helpers translate between the two.

// nfs4-acl-tools permission letter → FreeBSD letter, in FreeBSD's display order.
var freebsdPerms = []struct{ nfs4, bsd byte }{
	{'r', 'r'}, {'w', 'w'}, {'x', 'x'}, {'a', 'p'}, {'D', 'D'}, {'d', 'd'}, {'t', 'a'},
	{'T', 'A'}, {'n', 'R'}, {'N', 'W'}, {'c', 'c'}, {'C', 'C'}, {'o', 'o'}, {'y', 's'},
}

// Inheritance flag letters, identical in both forms ('g' is nfs4-only: it marks
// a group principal, which FreeBSD expresses with the "group:" tag instead).
const freebsdFlags = "fdinSFI"

var freebsdTypes = map[string]string{"A": "allow", "D": "deny", "U": "audit", "L": "alarm"}

var freebsdSpecial = map[string]string{"OWNER@": "owner@", "GROUP@": "group@", "EVERYONE@": "everyone@"}

func getFreeBSDNFSv4ACL(mountpoint string) ([]ACLEntry, error) {
	out, err := run("getfacl", "-q", mountpoint)
	if err != nil {
		return nil, fmt.Errorf("getfacl %s: %w", mountpoint, err)
	}
	var entries []ACLEntry
	for _, line := range splitLines(out) {
		if e, ok := parseFreeBSDNFSv4Line(strings.TrimSpace(line)); ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// parseFreeBSDNFSv4Line converts one FreeBSD getfacl NFSv4 line into the
// nfs4-acl-tools entry form.
func parseFreeBSDNFSv4Line(line string) (ACLEntry, bool) {
	f := strings.Split(line, ":")
	var e ACLEntry
	switch {
	case len(f) == 4 && strings.HasSuffix(f[0], "@"): // owner@:perms:flags:type
		e.Qualifier = strings.ToUpper(f[0])
		f = f[1:]
	case len(f) == 5 && (f[0] == "user" || f[0] == "group"): // user:alice:perms:flags:type
		e.Qualifier = f[1]
		if f[0] == "group" {
			e.Flags = "g"
		}
		f = f[2:]
	default:
		return ACLEntry{}, false
	}
	perms, flags, typ := f[0], f[1], f[2]
	for t, name := range freebsdTypes {
		if name == typ {
			e.Tag = t
		}
	}
	if e.Tag == "" {
		return ACLEntry{}, false
	}
	for _, p := range freebsdPerms {
		if strings.IndexByte(perms, p.bsd) >= 0 {
			e.Perms += string(p.nfs4)
		}
	}
	// FreeBSD pads flags with dashes ("fd-----"); orderFlags keeps known letters.
	e.Flags = orderFlags(e.Flags + flags)
	return e, true
}

// orderFlags returns the known flag letters of flags in canonical order, with
// the group-principal marker 'g' last.
func orderFlags(flags string) string {
	var b strings.Builder
	for i := range len(freebsdFlags) {
		if strings.IndexByte(flags, freebsdFlags[i]) >= 0 {
			b.WriteByte(freebsdFlags[i])
		}
	}
	if strings.Contains(flags, "g") {
		b.WriteByte('g')
	}
	return b.String()
}

// FreeBSDACESpec converts an nfs4-acl-tools entry into a FreeBSD setfacl
// NFSv4 entry ("user:alice:rwx:fd:allow"). A "@domain" suffix on a user or
// group principal is dropped — FreeBSD names local accounts directly.
func FreeBSDACESpec(e ACLEntry) (string, error) {
	typ, ok := freebsdTypes[e.Tag]
	if !ok {
		return "", fmt.Errorf("unsupported ACE type %q", e.Tag)
	}
	var perms strings.Builder
	for i := range len(e.Perms) {
		found := false
		for _, p := range freebsdPerms {
			if p.nfs4 == e.Perms[i] {
				perms.WriteByte(p.bsd)
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("unsupported permission %q", e.Perms[i])
		}
	}
	if perms.Len() == 0 {
		return "", fmt.Errorf("ACE grants no permissions")
	}
	var flags strings.Builder
	for i := range len(e.Flags) {
		c := e.Flags[i]
		switch {
		case c == 'g':
		case strings.IndexByte(freebsdFlags, c) >= 0:
			flags.WriteByte(c)
		default:
			return "", fmt.Errorf("unsupported flag %q", c)
		}
	}
	var tag string
	if special, ok := freebsdSpecial[strings.ToUpper(e.Qualifier)]; ok {
		tag = special
	} else {
		name, _, _ := strings.Cut(e.Qualifier, "@")
		if name == "" {
			return "", fmt.Errorf("ACE has no principal")
		}
		tag = "user:" + name
		if strings.Contains(e.Flags, "g") {
			tag = "group:" + name
		}
	}
	return tag + ":" + perms.String() + ":" + flags.String() + ":" + typ, nil
}
