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

// getNFSv4ACL runs nfs4_getfacl and parses the output.
func getNFSv4ACL(mountpoint string) ([]ACLEntry, error) {
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
