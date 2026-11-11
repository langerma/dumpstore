package ansible

import (
	"os"
	"strings"
	"testing"
)

func TestWriteExtraVarsFormat(t *testing.T) {
	path, err := writeExtraVars(map[string]string{
		"b_key":   `q"b\s`,
		"a_key":   "line1\nline2\ttab",
		"tmpl":    "{{ 7*6 }} {% if true %}x{% endif %}",
		"yamlish": "# not: a comment",
		"html":    "<a&b>",
		"uni":     "ünï \u2028 €",
		"empty":   "",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sorted keys, one root !unsafe tag, JSON-escaped values (no HTML escaping;
	// U+2028 is escaped by encoding/json, which YAML reads back identically).
	want := `--- !unsafe
a_key: "line1\nline2\ttab"
b_key: "q\"b\\s"
empty: ""
html: "<a&b>"
tmpl: "{{ 7*6 }} {% if true %}x{% endif %}"
uni: "ünï \u2028 €"
yamlish: "# not: a comment"
`
	if string(got) != want {
		t.Errorf("file content:\n%s\nwant:\n%s", got, want)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
	if !strings.HasPrefix(fi.Name(), "dumpstore-extravars-") {
		t.Errorf("name = %q", fi.Name())
	}
}

func TestWriteExtraVarsRejectsInvalidKey(t *testing.T) {
	for _, k := range []string{"", "Upper", "1st", "a-b", "a.b", "a: b", "x\ny", "com.sun:auto-snapshot"} {
		if path, err := writeExtraVars(map[string]string{k: "v"}); err == nil {
			os.Remove(path)
			t.Errorf("key %q accepted", k)
		}
	}
}
