package ansible

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRunnerValuesAreInert runs a real ansible-playbook (skipped when it is
// not installed) and checks that extra-var values reach the playbook as plain,
// untemplated strings (#214) of the right type.
func TestRunnerValuesAreInert(t *testing.T) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-playbook not in PATH")
	}

	base, out := t.TempDir(), t.TempDir()
	pbDir := filepath.Join(base, "playbooks")
	cb, err := os.ReadFile("../../playbooks/callback_plugins/ndjson.py")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"callback_plugins/ndjson.py": string(cb),
		"inventory/localhost":        "localhost ansible_connection=local ansible_python_interpreter=auto_silent\n",
		"inert.yml": `- hosts: localhost
  gather_facts: false
  tasks:
    - name: Write values
      copy:
        content: "{{ lookup('vars', item) }}"
        dest: "{{ out }}/{{ item }}"
      loop: [tmpl, stmt, pipe, tricky]
    - name: Write from_json element
      copy:
        content: "{{ (list | from_json)[0] }}"
        dest: "{{ out }}/list0"
    - name: Check types and comparisons
      assert:
        that:
          - yes_ == "true"
          - yes_ | bool
          - no_ == "false"
          - not (no_ | bool)
          - empty == ""
          - not (empty != "")
          - num == "123"
          - num is string
          - (list | from_json) | length == 2
`,
	}
	for name, content := range files {
		p := filepath.Join(pbDir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	written := map[string]string{
		"tmpl":   "{{ 7*6 }}",
		"stmt":   "{% if true %}x{% endif %}",
		"pipe":   "{{ lookup('pipe', 'touch " + out + "/pwned') }}",
		"tricky": "q\"uote 'single' back\\slash\nnew\ttab ünï   # a: b",
	}
	vars := map[string]string{
		"out":   out,
		"yes_":  "true",
		"no_":   "false",
		"empty": "",
		"num":   "123",
		"list":  `["{{ 1+1 }}", "b"]`,
	}
	for k, v := range written {
		vars[k] = v
	}

	r := NewRunner(base)
	r.Timeout = 2 * time.Minute
	if _, err := r.Run(context.Background(), "inert.yml", vars); err != nil {
		t.Fatalf("playbook failed: %v", err)
	}

	for k, want := range written {
		got, err := os.ReadFile(filepath.Join(out, k))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s: got %q, want %q", k, got, want)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(out, "list0")); string(got) != "{{ 1+1 }}" {
		t.Errorf("from_json element: got %q, want literal {{ 1+1 }}", got)
	}
	if _, err := os.Stat(filepath.Join(out, "pwned")); !os.IsNotExist(err) {
		t.Error("lookup('pipe') in a value was executed")
	}
}
