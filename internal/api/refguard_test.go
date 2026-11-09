package api

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"dumpstore/internal/iscsi"
	"dumpstore/internal/replication"
	"dumpstore/internal/smb"
	"dumpstore/internal/zfs"
)

// fs and vol build test datasets.
func fs(name, mp string) zfs.Dataset {
	return zfs.Dataset{Name: name, Type: "filesystem", Mountpoint: mp}
}

func vol(name string) zfs.Dataset {
	return zfs.Dataset{Name: name, Type: "volume", Mountpoint: "-"}
}

// baseDatasets is the layout most cases run against:
//
//	tank               /tank
//	tank/data          /tank/data
//	tank/data/child    /tank/data/child   (inherited)
//	tank/data/own      /srv/own           (local)
//	tank/data/own/sub  /srv/own/sub       (inherited from own)
//	tank/data2         /tank/data2
//	tank/local         /mnt/local         (local)
//	tank/local/kid     /mnt/local/kid     (inherited from local)
//	tank/legacy        legacy
//	tank/none          none
//	tank/vol           -
var baseDatasets = []zfs.Dataset{
	fs("tank", "/tank"),
	fs("tank/data", "/tank/data"),
	fs("tank/data/child", "/tank/data/child"),
	fs("tank/data/own", "/srv/own"),
	fs("tank/data/own/sub", "/srv/own/sub"),
	fs("tank/data2", "/tank/data2"),
	fs("tank/local", "/mnt/local"),
	fs("tank/local/kid", "/mnt/local/kid"),
	fs("tank/legacy", "legacy"),
	fs("tank/none", "none"),
	vol("tank/vol"),
}

func shares(pairs ...string) []smb.Usershare {
	var out []smb.Usershare
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, smb.Usershare{Name: pairs[i], Path: pairs[i+1]})
	}
	return out
}

func us(name, ds, p string) refConflict {
	return refConflict{Kind: kindUsershare, Dataset: ds, Name: name, Path: p, Describe: name + " (" + p + ")"}
}

func TestFindConflicts(t *testing.T) {
	allShares := shares(
		"data", "/tank/data",
		"data2", "/tank/data2",
		"datasub", "/tank/data/some/dir",
		"child", "/tank/data/child",
		"own", "/srv/own",
		"ownsub", "/srv/own/sub",
		"local", "/mnt/local",
		"kid", "/mnt/local/kid/x",
		"elsewhere", "/opt/x",
		"relative", "tank/data",
	)
	tests := []struct {
		name      string
		s         refSnapshot
		op        refOp
		target    string
		recursive bool
		newMP     string
		want      []refConflict
	}{
		{
			name: "destroy: prefix sibling and nested datasets don't count, subdirectory does",
			s:    refSnapshot{Datasets: baseDatasets, Usershares: allShares},
			op:   refDestroy, target: "tank/data",
			want: []refConflict{us("data", "tank/data", "/tank/data"), us("datasub", "tank/data", "/tank/data/some/dir")},
		},
		{
			name: "destroy recursive: whole subtree, including own-mountpoint children",
			s:    refSnapshot{Datasets: baseDatasets, Usershares: allShares},
			op:   refDestroy, target: "tank/data", recursive: true,
			want: []refConflict{
				us("data", "tank/data", "/tank/data"),
				us("datasub", "tank/data", "/tank/data/some/dir"),
				us("child", "tank/data/child", "/tank/data/child"),
				us("own", "tank/data/own", "/srv/own"),
				us("ownsub", "tank/data/own/sub", "/srv/own/sub"),
			},
		},
		{
			name: "destroy leaf with own mountpoint",
			s:    refSnapshot{Datasets: baseDatasets, Usershares: allShares},
			op:   refDestroy, target: "tank/data/own",
			want: []refConflict{us("own", "tank/data/own", "/srv/own")},
		},
		{
			name: "destroy unreferenced dataset",
			s:    refSnapshot{Datasets: baseDatasets, Usershares: allShares},
			op:   refDestroy, target: "tank/data2/x",
			want: nil,
		},
		{
			name: "rename: inherited subtree moves, own mountpoints don't, names change",
			s: refSnapshot{
				Datasets:   baseDatasets,
				Usershares: allShares,
				Tasks:      []replication.Task{{ID: "r1", Name: "own-repl", Source: "tank/data/own/sub", Remote: "u@h"}},
			},
			op: refRename, target: "tank/data",
			want: []refConflict{
				us("data", "tank/data", "/tank/data"),
				us("datasub", "tank/data", "/tank/data/some/dir"),
				us("child", "tank/data/child", "/tank/data/child"),
				{Kind: kindReplSource, Dataset: "tank/data/own/sub", Name: "own-repl", ID: "r1", Describe: "own-repl (source)"},
			},
		},
		{
			name: "rename of a local-mountpoint dataset only blocks name references",
			s: refSnapshot{
				Datasets:   baseDatasets,
				Usershares: allShares,
				Targets:    []iscsi.Target{{IQN: "iqn.x", ZvolName: "tank/local/kid"}},
			},
			op: refRename, target: "tank/local",
			want: []refConflict{{Kind: kindISCSI, Dataset: "tank/local/kid", Name: "iqn.x", Describe: "iqn.x"}},
		},
		{
			name: "rename with unknown parent treats subtree as moving",
			s:    refSnapshot{Datasets: baseDatasets[1:], Usershares: allShares},
			op:   refRename, target: "tank/local",
			want: []refConflict{us("local", "tank/local", "/mnt/local"), us("kid", "tank/local/kid", "/mnt/local/kid/x")},
		},
		{
			name: "mountpoint: dataset and inheriting children move, own mountpoints don't",
			s: refSnapshot{
				Datasets:   baseDatasets,
				Usershares: allShares,
				Tasks:      []replication.Task{{ID: "r1", Name: "t", Source: "tank/data"}},
				Targets:    []iscsi.Target{{IQN: "iqn.x", ZvolName: "tank/vol"}},
			},
			op: refMountpoint, target: "tank/data", newMP: "/elsewhere",
			want: []refConflict{
				us("data", "tank/data", "/tank/data"),
				us("datasub", "tank/data", "/tank/data/some/dir"),
				us("child", "tank/data/child", "/tank/data/child"),
			},
		},
		{
			name: "mountpoint inherit (empty value) counts as a change",
			s:    refSnapshot{Datasets: baseDatasets, Usershares: allShares},
			op:   refMountpoint, target: "tank/local", newMP: "",
			want: []refConflict{us("local", "tank/local", "/mnt/local"), us("kid", "tank/local/kid", "/mnt/local/kid/x")},
		},
		{
			name: "mountpoint unchanged is a no-op",
			s:    refSnapshot{Datasets: baseDatasets, Usershares: allShares},
			op:   refMountpoint, target: "tank/data", newMP: "/tank/data/",
			want: nil,
		},
		{
			name: "mountpoint on legacy/none datasets moves nothing",
			s:    refSnapshot{Datasets: baseDatasets, Usershares: shares("l", "/tank/legacy")},
			op:   refMountpoint, target: "tank/legacy", newMP: "/x",
			want: nil,
		},
		{
			name: "legacy, none and volumes never own paths",
			s: refSnapshot{
				Datasets:   baseDatasets,
				Usershares: shares("l", "/tank/legacy", "n", "/tank/none/x"),
			},
			op: refDestroy, target: "tank/legacy",
			want: nil,
		},
		{
			name: "path under a legacy dataset belongs to its path-owning ancestor",
			s: refSnapshot{
				Datasets:   baseDatasets,
				Usershares: shares("l", "/tank/legacy"),
			},
			op: refDestroy, target: "tank", recursive: true,
			want: []refConflict{us("l", "tank", "/tank/legacy")},
		},
		{
			name: "root mountpoint and ties: every owner of the path counts",
			s: refSnapshot{
				Datasets: []zfs.Dataset{
					fs("rpool", "/"), fs("rpool/ROOT", "none"), fs("rpool/ROOT/a", "/"), fs("rpool/ROOT/b", "/"),
				},
				Usershares: shares("srv", "/srv/share"),
			},
			op: refDestroy, target: "rpool/ROOT/b",
			want: []refConflict{us("srv", "rpool/ROOT/b", "/srv/share")},
		},
		{
			name: "homes base is cut at the first substitution",
			s: refSnapshot{
				Datasets:  baseDatasets,
				HomesPath: "/tank/data/homes/%U",
			},
			op: refDestroy, target: "tank/data",
			want: []refConflict{{Kind: kindHomes, Dataset: "tank/data", Name: "homes", Path: "/tank/data/homes/%U", Describe: "[homes] (/tank/data/homes/%U)"}},
		},
		{
			name: "homes with %S in the middle",
			s: refSnapshot{
				Datasets:  baseDatasets,
				HomesPath: "/tank/data2/%S/files",
			},
			op: refDestroy, target: "tank/data2",
			want: []refConflict{{Kind: kindHomes, Dataset: "tank/data2", Name: "homes", Path: "/tank/data2/%S/files", Describe: "[homes] (/tank/data2/%S/files)"}},
		},
		{
			name: "homes on another dataset doesn't block",
			s: refSnapshot{
				Datasets:  baseDatasets,
				HomesPath: "/tank/data2/%U",
			},
			op: refDestroy, target: "tank/data",
			want: nil,
		},
		{
			name: "time machine",
			s: refSnapshot{
				Datasets:    baseDatasets,
				TimeMachine: []smb.TimeMachineShare{{Name: "tm", Path: "/tank/data/tm"}, {Name: "other", Path: "/tank/data2"}},
			},
			op: refDestroy, target: "tank/data",
			want: []refConflict{{Kind: kindTimeMachine, Dataset: "tank/data", Name: "tm", Path: "/tank/data/tm", Describe: "tm (/tank/data/tm)"}},
		},
		{
			name: "iscsi zvol in and out of the subtree",
			s: refSnapshot{
				Datasets: append(append([]zfs.Dataset{}, baseDatasets...), vol("tank/data/vm")),
				Targets:  []iscsi.Target{{IQN: "iqn.a", ZvolName: "tank/data/vm"}, {IQN: "iqn.b", ZvolName: "tank/vol"}},
			},
			op: refDestroy, target: "tank/data", recursive: true,
			want: []refConflict{{Kind: kindISCSI, Dataset: "tank/data/vm", Name: "iqn.a", Describe: "iqn.a"}},
		},
		{
			name: "iscsi zvol destroy",
			s: refSnapshot{
				Datasets: baseDatasets,
				Targets:  []iscsi.Target{{IQN: "iqn.b", ZvolName: "tank/vol"}},
			},
			op: refDestroy, target: "tank/vol",
			want: []refConflict{{Kind: kindISCSI, Dataset: "tank/vol", Name: "iqn.b", Describe: "iqn.b"}},
		},
		{
			name: "replication: source and local target block, ancestor source and remote target don't",
			s: refSnapshot{
				Datasets: baseDatasets,
				Tasks: []replication.Task{
					{ID: "a", Name: "src", Source: "tank/data/child", Target: "backup/x"},
					{ID: "b", Name: "ancestor", Source: "tank", Target: "backup/y", Recursive: true},
					{ID: "c", Name: "local-dst", Source: "tank/data2", Target: "tank/data/child"},
					{ID: "d", Name: "remote-dst", Source: "tank/data2", Target: "tank/data/child", Remote: "u@h"},
				},
			},
			op: refDestroy, target: "tank/data", recursive: true,
			want: []refConflict{
				{Kind: kindReplSource, Dataset: "tank/data/child", Name: "src", ID: "a", Describe: "src (source)"},
				{Kind: kindReplTarget, Dataset: "tank/data/child", Name: "local-dst", ID: "c", Describe: "local-dst (target)"},
			},
		},
		{
			name: "replication unaffected by mountpoint change",
			s: refSnapshot{
				Datasets: baseDatasets,
				Tasks:    []replication.Task{{ID: "a", Name: "src", Source: "tank/data2", Target: "backup/x"}},
			},
			op: refMountpoint, target: "tank/data2", newMP: "/x",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findConflicts(tt.s, tt.op, tt.target, tt.recursive, tt.newMP)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestFindConflictsStableOrder(t *testing.T) {
	s := refSnapshot{
		Datasets:    append(append([]zfs.Dataset{}, baseDatasets...), vol("tank/data/vm")),
		Usershares:  shares("b", "/tank/data/child", "a", "/tank/data", "c", "/tank/data/x"),
		HomesPath:   "/tank/data/homes/%U",
		TimeMachine: []smb.TimeMachineShare{{Name: "tm", Path: "/srv/own"}},
		Targets:     []iscsi.Target{{IQN: "iqn.a", ZvolName: "tank/data/vm"}},
		Tasks: []replication.Task{
			{ID: "2", Name: "z", Source: "tank/data"},
			{ID: "1", Name: "y", Source: "tank/data/child", Target: "tank/data/own"},
		},
	}
	want := findConflicts(s, refDestroy, "tank/data", true, "")
	if len(want) != 9 {
		t.Fatalf("expected 9 conflicts, got %d: %+v", len(want), want)
	}
	kinds := make([]string, len(want))
	for i, c := range want {
		kinds[i] = c.Kind
	}
	if got := strings.Join(kinds, ","); got != "smb_usershare,smb_usershare,smb_usershare,smb_homes,smb_timemachine,iscsi_target,replication_source,replication_source,replication_target" {
		t.Fatalf("kind order: %s", got)
	}
	rng := rand.New(rand.NewSource(1))
	for range 20 {
		shuffled := s
		shuffled.Datasets = append([]zfs.Dataset{}, s.Datasets...)
		shuffled.Usershares = append([]smb.Usershare{}, s.Usershares...)
		shuffled.Tasks = append([]replication.Task{}, s.Tasks...)
		rng.Shuffle(len(shuffled.Datasets), func(i, j int) {
			shuffled.Datasets[i], shuffled.Datasets[j] = shuffled.Datasets[j], shuffled.Datasets[i]
		})
		rng.Shuffle(len(shuffled.Usershares), func(i, j int) {
			shuffled.Usershares[i], shuffled.Usershares[j] = shuffled.Usershares[j], shuffled.Usershares[i]
		})
		rng.Shuffle(len(shuffled.Tasks), func(i, j int) {
			shuffled.Tasks[i], shuffled.Tasks[j] = shuffled.Tasks[j], shuffled.Tasks[i]
		})
		if got := findConflicts(shuffled, refDestroy, "tank/data", true, ""); !reflect.DeepEqual(got, want) {
			t.Fatalf("order depends on input order:\ngot  %+v\nwant %+v", got, want)
		}
	}
}

func TestWriteConflictsText(t *testing.T) {
	c := []refConflict{
		us("data", "tank/data", "/tank/data"),
		{Kind: kindReplSource, Dataset: "tank/data", Name: "nightly", ID: "r1"},
	}
	w := httptest.NewRecorder()
	writeConflicts(t.Context(), w, refDestroy, "tank/data", c)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d", w.Code)
	}
	want := "cannot destroy tank/data: still in use by dumpstore-managed configuration:\n" +
		"  - SMB share \"data\" (/tank/data)\n" +
		"  - replication task \"nightly\" (source tank/data)\n" +
		"Remove these references first, then retry."
	var body apiError
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error != want {
		t.Fatalf("error text:\n%s\nwant:\n%s", body.Error, want)
	}
	if !reflect.DeepEqual(body.Conflicts, c) {
		t.Fatalf("conflicts: %+v", body.Conflicts)
	}
}
