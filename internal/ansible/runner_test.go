package ansible

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeSecret = "s3cret-{{ sentinel }}"

// fakeAnsible puts an ansible-playbook stub on PATH that records its argv,
// the mode and content of the `--extra-vars @file`, and exits with rc.
func fakeAnsible(t *testing.T, rc int) (r *Runner, rec string) {
	t.Helper()
	bin, rec := t.TempDir(), t.TempDir()
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + rec + `/argv"
prev=
for a in "$@"; do
  if [ "$prev" = "--extra-vars" ]; then
    f="${a#@}"
    ls -l "$f" | cut -c1-10 > "` + rec + `/mode"
    cp "$f" "` + rec + `/vars"
    printf '%s' "$f" > "` + rec + `/path"
  fi
  prev="$a"
done
echo '{"task":"t","status":"ok","msg":""}'
exit ` + string(rune('0'+rc)) + `
`
	if err := os.WriteFile(filepath.Join(bin, "ansible-playbook"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &Runner{PlaybookDir: "pb", InventoryPath: "inv", Timeout: time.Minute}, rec
}

func readRec(t *testing.T, rec, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(rec, name))
	if err != nil {
		t.Fatalf("stub did not record %s: %v", name, err)
	}
	return string(b)
}

func TestRunnerExtraVarsOffArgv(t *testing.T) {
	for _, rc := range []int{0, 1} {
		r, rec := fakeAnsible(t, rc)
		_, err := r.Run(context.Background(), "x.yml", map[string]string{"username": "bob", "password": fakeSecret})
		if (err != nil) != (rc != 0) {
			t.Fatalf("rc=%d: err = %v", rc, err)
		}

		argv := readRec(t, rec, "argv")
		if strings.Contains(argv, "s3cret") || strings.Contains(argv, "bob") {
			t.Errorf("rc=%d: a value leaked into argv:\n%s", rc, argv)
		}
		if mode := strings.TrimSpace(readRec(t, rec, "mode")); mode != "-rw-------" {
			t.Errorf("rc=%d: vars file mode during run = %s, want -rw-------", rc, mode)
		}
		if vars := readRec(t, rec, "vars"); !strings.Contains(vars, `password: "s3cret-{{ sentinel }}"`) {
			t.Errorf("rc=%d: vars file lacks the value:\n%s", rc, vars)
		}
		if _, err := os.Stat(readRec(t, rec, "path")); !os.IsNotExist(err) {
			t.Errorf("rc=%d: vars file not removed after run (stat err %v)", rc, err)
		}
	}
}

func TestRunnerNoExtraVars(t *testing.T) {
	r, rec := fakeAnsible(t, 0)
	if _, err := r.Run(context.Background(), "x.yml", nil); err != nil {
		t.Fatal(err)
	}
	if argv := readRec(t, rec, "argv"); strings.Contains(argv, "--extra-vars") {
		t.Errorf("--extra-vars passed without vars:\n%s", argv)
	}
}
