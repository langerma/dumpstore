package auth

import (
	"strings"
	"testing"
)

func TestHashPasswordFormat(t *testing.T) {
	hash, err := hashPassword([]byte("hunter2"))
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("expected PHC prefix, got %q", hash[:min(len(hash), 20)])
	}
}

func TestVerifyPasswordCorrect(t *testing.T) {
	hash, err := hashPassword([]byte("correct-horse"))
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if err := verifyPassword(hash, []byte("correct-horse")); err != nil {
		t.Errorf("verifyPassword with correct password: %v", err)
	}
}

func TestVerifyPasswordWrong(t *testing.T) {
	hash, err := hashPassword([]byte("correct-horse"))
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if err := verifyPassword(hash, []byte("wrong")); err == nil {
		t.Error("verifyPassword with wrong password: expected error, got nil")
	}
}

func TestRoundTrip(t *testing.T) {
	passwords := []string{"simple", "P@$$w0rd!", "a", strings.Repeat("x", 72)}
	for _, pw := range passwords {
		hash, err := hashPassword([]byte(pw))
		if err != nil {
			t.Fatalf("hashPassword(%q): %v", pw, err)
		}
		if err := verifyPassword(hash, []byte(pw)); err != nil {
			t.Errorf("round-trip failed for %q: %v", pw, err)
		}
	}
}

func TestHashesAreUnique(t *testing.T) {
	h1, _ := hashPassword([]byte("same"))
	h2, _ := hashPassword([]byte("same"))
	if h1 == h2 {
		t.Error("two hashes of the same password should differ (random salt)")
	}
}

func TestVerifyBcryptHashReturnsError(t *testing.T) {
	bcryptHash := "$2b$12$somefakebcrypthashvalue.thatislong.enough"
	err := verifyPassword(bcryptHash, []byte("anything"))
	if err == nil {
		t.Error("expected error for bcrypt hash, got nil")
	}
}

func TestVerifyInvalidHashReturnsError(t *testing.T) {
	for _, bad := range []string{"", "notaphcstring", "$argon2id$garbage"} {
		if err := verifyPassword(bad, []byte("pw")); err == nil {
			t.Errorf("verifyPassword(%q): expected error, got nil", bad)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
