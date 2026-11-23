package replication

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeZFS replaces runOut/runCmd: list returns the given snapshot names, every
// other command is recorded; destroy fails for names in busy.
func fakeZFS(t *testing.T, list []string, busy ...string) *[]string {
	t.Helper()
	var cmds []string
	oldOut, oldCmd := runOut, runCmd
	t.Cleanup(func() { runOut, runCmd = oldOut, oldCmd })
	runOut = func(name string, args ...string) ([]byte, error) {
		return []byte(strings.Join(list, "\n") + "\n"), nil
	}
	runCmd = func(name string, args ...string) error {
		c := name + " " + strings.Join(args, " ")
		cmds = append(cmds, c)
		for _, b := range busy {
			if strings.HasSuffix(c, " "+b) {
				return errors.New("dataset is busy")
			}
		}
		return nil
	}
	return &cmds
}

const (
	task  = "aaaaaaaaaaaaaaaa"
	other = "bbbbbbbbbbbbbbbb"
)

func own(ts string) string    { return "tank/data@dumpstore-repl-" + task + "-" + ts }
func their(ts string) string  { return "tank/data@dumpstore-repl-" + other + "-" + ts }
func legacy(ts string) string { return "tank/data@dumpstore-repl-" + ts }

func TestSnapNameCarriesTaskID(t *testing.T) {
	got := snapName(time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC), task)
	if got != "dumpstore-repl-"+task+"-2026-11-23_09-30-00" {
		t.Fatalf("snapName = %q", got)
	}
}

func TestPruneSourceOnlyOwnOldest(t *testing.T) {
	list := []string{ // oldest first, as `zfs list -s creation`
		legacy("20261101T000000Z"),
		own("2026-11-02_00-00-00"),
		their("2026-11-02_00-00-01"),
		own("2026-11-03_00-00-00"),
		own("2026-11-04_00-00-00"),
		their("2026-11-04_00-00-01"),
		own("2026-11-05_00-00-00"),
		"tank/data@manual",
		"tank/data/child@dumpstore-repl-" + task + "-2026-11-02_00-00-00", // child: not this dataset
	}
	cmds := fakeZFS(t, list)
	destroyed, err := pruneSource("tank/data", task, false, 2, own("2026-11-05_00-00-00"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{own("2026-11-02_00-00-00"), own("2026-11-03_00-00-00")}
	if !slices.Equal(destroyed, want) {
		t.Fatalf("destroyed %v, want %v", destroyed, want)
	}
	for _, c := range *cmds {
		if strings.Contains(c, other) || strings.Contains(c, "@manual") || !strings.Contains(c, "@dumpstore-repl-"+task+"-") {
			t.Errorf("destroyed something not its own: %q", c)
		}
		if strings.Contains(c, " -r ") {
			t.Errorf("non-recursive task used -r: %q", c)
		}
	}
}

func TestPruneSourceNeverDestroysKeep(t *testing.T) {
	// keep is unusually not the newest (e.g. a later run's snapshot appeared);
	// it must survive anyway.
	list := []string{own("2026-11-01_00-00-00"), own("2026-11-02_00-00-00"), own("2026-11-03_00-00-00")}
	fakeZFS(t, list)
	destroyed, err := pruneSource("tank/data", task, false, 1, own("2026-11-01_00-00-00"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(destroyed, []string{own("2026-11-02_00-00-00")}) {
		t.Fatalf("destroyed %v", destroyed)
	}
}

func TestPruneSourceRetainAtLeastOne(t *testing.T) {
	list := []string{own("2026-11-01_00-00-00"), own("2026-11-02_00-00-00")}
	fakeZFS(t, list)
	for _, retain := range []int{0, -3} {
		destroyed, err := pruneSource("tank/data", task, false, retain, own("2026-11-02_00-00-00"))
		if err != nil || !slices.Equal(destroyed, []string{own("2026-11-01_00-00-00")}) {
			t.Errorf("retain %d: destroyed %v, err %v — the newest snapshot must survive", retain, destroyed, err)
		}
	}
}

func TestPruneSourceRecursiveUsesR(t *testing.T) {
	list := []string{own("2026-11-01_00-00-00"), own("2026-11-02_00-00-00")}
	cmds := fakeZFS(t, list)
	if _, err := pruneSource("tank/data", task, true, 1, own("2026-11-02_00-00-00")); err != nil {
		t.Fatal(err)
	}
	if len(*cmds) != 1 || (*cmds)[0] != "zfs destroy -r "+own("2026-11-01_00-00-00") {
		t.Fatalf("commands %v", *cmds)
	}
}

func TestPruneSourceSkipsBusyAndContinues(t *testing.T) {
	list := []string{own("2026-11-01_00-00-00"), own("2026-11-02_00-00-00"), own("2026-11-03_00-00-00")}
	fakeZFS(t, list, own("2026-11-01_00-00-00"))
	destroyed, err := pruneSource("tank/data", task, false, 1, own("2026-11-03_00-00-00"))
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Errorf("err = %v, want the busy error reported", err)
	}
	if !slices.Equal(destroyed, []string{own("2026-11-02_00-00-00")}) {
		t.Fatalf("destroyed %v, want the non-busy one", destroyed)
	}
}

func TestPruneSourceNothingToDo(t *testing.T) {
	cmds := fakeZFS(t, []string{legacy("20261101T000000Z"), their("2026-11-02_00-00-00"), own("2026-11-03_00-00-00")})
	destroyed, err := pruneSource("tank/data", task, false, 1, own("2026-11-03_00-00-00"))
	if err != nil || len(destroyed) != 0 || len(*cmds) != 0 {
		t.Fatalf("destroyed %v, cmds %v, err %v", destroyed, *cmds, err)
	}
}
