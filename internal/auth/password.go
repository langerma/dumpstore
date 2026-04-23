package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters — OWASP minimum recommended values.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

var errBcryptHash = errors.New("bcrypt hash detected")

// hashPassword hashes password with argon2id and returns a PHC string:
//
//	$argon2id$v=19$m=65536,t=3,p=4$<base64-salt>$<base64-hash>
func hashPassword(password []byte) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	hash := argon2.IDKey(password, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return encodePHC(salt, hash), nil
}

// verifyPassword checks password against a PHC string produced by hashPassword.
// If hash looks like a bcrypt hash, it logs a warning and returns an error so
// the operator knows to run --set-password after upgrading.
func verifyPassword(hash string, password []byte) error {
	if strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") {
		slog.Warn("bcrypt password hash detected — run --set-password to reset after upgrading")
		return errors.New("unsupported hash format")
	}
	salt, expected, err := decodePHC(hash)
	if err != nil {
		return fmt.Errorf("parse hash: %w", err)
	}
	got := argon2.IDKey(password, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	if subtle.ConstantTimeCompare(got, expected) != 1 {
		return errors.New("password mismatch")
	}
	return nil
}

// encodePHC encodes salt and hash as a PHC string.
func encodePHC(salt, hash []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt),
		b64.EncodeToString(hash),
	)
}

// decodePHC parses a PHC string and returns the salt and hash.
// Only the fixed parameters produced by hashPassword are accepted.
func decodePHC(s string) (salt, hash []byte, err error) {
	// expected: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return nil, nil, errors.New("invalid PHC format")
	}

	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return nil, nil, errors.New("unsupported argon2 version")
	}

	var m, t, p uint32
	params := parts[3]
	for _, kv := range strings.Split(params, ",") {
		pair := strings.SplitN(kv, "=", 2)
		if len(pair) != 2 {
			return nil, nil, errors.New("invalid params")
		}
		n, err := strconv.ParseUint(pair[1], 10, 32)
		if err != nil {
			return nil, nil, fmt.Errorf("param %s: %w", pair[0], err)
		}
		switch pair[0] {
		case "m":
			m = uint32(n)
		case "t":
			t = uint32(n)
		case "p":
			p = uint32(n)
		}
	}
	if m != argonMemory || t != argonTime || p != argonThreads {
		return nil, nil, errors.New("unexpected argon2id parameters")
	}

	b64 := base64.RawStdEncoding
	salt, err = b64.DecodeString(parts[4])
	if err != nil {
		return nil, nil, fmt.Errorf("decode salt: %w", err)
	}
	hash, err = b64.DecodeString(parts[5])
	if err != nil {
		return nil, nil, fmt.Errorf("decode hash: %w", err)
	}
	return salt, hash, nil
}
