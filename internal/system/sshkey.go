package system

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// AuthorizedKey is one entry of an authorized_keys file.
type AuthorizedKey struct {
	Key         string `json:"key" doc:"The complete line, as stored"`
	Type        string `json:"type" doc:"Key type, e.g. ssh-ed25519; empty if the line could not be parsed"`
	Fingerprint string `json:"fingerprint" doc:"OpenSSH SHA256 fingerprint (as printed by ssh-keygen -l); empty if the line could not be parsed"`
	Comment     string `json:"comment"`
}

// ParseAuthorizedKey splits an authorized_keys line into type, key blob and
// comment, skipping a leading options field (from="…",command="…" …), and
// computes the OpenSSH SHA256 fingerprint of the blob. Lines that don't
// parse keep only Key.
func ParseAuthorizedKey(line string) AuthorizedKey {
	k := AuthorizedKey{Key: line}
	fields := strings.Fields(line)
	for i := 0; i+1 < len(fields); i++ {
		blob, err := base64.StdEncoding.DecodeString(fields[i+1])
		if err != nil || !isKeyType(fields[i]) || !blobHasType(blob, fields[i]) {
			continue
		}
		sum := sha256.Sum256(blob)
		k.Type = fields[i]
		k.Fingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
		k.Comment = strings.Join(fields[i+2:], " ")
		return k
	}
	return k
}

func isKeyType(s string) bool {
	return strings.HasPrefix(s, "ssh-") || strings.HasPrefix(s, "ecdsa-") || strings.HasPrefix(s, "sk-")
}

// blobHasType checks the wire-format blob starts with the same type string
// (uint32 length + name), so an options field that happens to look like a
// key type is not mistaken for one.
func blobHasType(blob []byte, typ string) bool {
	if len(blob) < 4 {
		return false
	}
	n := int(blob[0])<<24 | int(blob[1])<<16 | int(blob[2])<<8 | int(blob[3])
	return n == len(typ) && len(blob) >= 4+n && string(blob[4:4+n]) == typ
}
