package ansible

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
)

// extraVarKey is the shape of every extra-var name. Keys are defined in Go
// code, so a key outside it is a programming error.
var extraVarKey = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// writeExtraVars writes vars to a new 0600 temp file for `-e @file` and
// returns its path; the caller removes it after the run.
//
// The whole mapping carries one `!unsafe` tag, so Ansible treats every value
// as data and never evaluates {{ … }} / {% … %} inside it (#214). The tag sits
// on the root, not on each value: ansible-core ≥ 2.19 re-resolves a tagged
// scalar as if it were unquoted, turning !unsafe "true" into a bool and
// !unsafe "" into None. Children of a tagged mapping keep their own,
// quote-aware resolution, so every value stays a string on 2.16 and 2.20.
//
// Values are JSON-encoded: JSON string escapes are valid YAML double-quoted
// escapes, so every valid UTF-8 string round-trips exactly (invalid bytes
// become U+FFFD, as with the previous json.Marshal argv form).
func writeExtraVars(vars map[string]string) (string, error) {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		if !extraVarKey.MatchString(k) {
			return "", fmt.Errorf("invalid extra-var name %q", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("--- !unsafe\n")
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, k := range keys {
		buf.WriteString(k + ": ")
		if err := enc.Encode(vars[k]); err != nil { // Encode appends "\n"
			return "", fmt.Errorf("encode extra-var %q: %w", k, err)
		}
	}

	f, err := os.CreateTemp("", "dumpstore-extravars-*.yml") // 0600, O_EXCL
	if err != nil {
		return "", fmt.Errorf("create extra-vars file: %w", err)
	}
	_, werr := f.Write(buf.Bytes())
	if err := errors.Join(werr, f.Close()); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("write extra-vars file: %w", err)
	}
	return f.Name(), nil
}
