package zfs

import (
	"testing"
)

func TestParseUint(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want uint64
	}{
		{"dash", "-", 0},
		{"empty", "", 0},
		{"none", "none", 0},
		{"zero", "0", 0},
		{"positive", "12345", 12345},
		{"large", "18446744073709551615", 18446744073709551615},
		{"leading_space", "  42  ", 42},
		{"tab_space", "\t 100 \t", 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUint(tt.in)
			if got != tt.want {
				t.Errorf("parseUint(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseInt64(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int64
	}{
		{"dash", "-", 0},
		{"empty", "", 0},
		{"zero", "0", 0},
		{"positive", "1710000000", 1710000000},
		{"negative", "-100", -100},
		{"leading_space", "  999  ", 999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseInt64(tt.in)
			if got != tt.want {
				t.Errorf("parseInt64(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseFloat(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want float64
	}{
		{"dash", "-", 0},
		{"empty", "", 0},
		{"zero", "0", 0},
		{"integer", "42", 42},
		{"decimal", "3.14", 3.14},
		{"leading_space", "  1.5  ", 1.5},
		{"large", "1000000.5", 1000000.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFloat(tt.in)
			if got != tt.want {
				t.Errorf("parseFloat(%q) = %f, want %f", tt.in, got, tt.want)
			}
		})
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int // expected number of lines
	}{
		{"empty", "", 0},
		{"single_line", "hello", 1},
		{"trailing_newline", "hello\n", 1},
		{"multiple_trailing_newlines", "hello\n\n\n", 1},
		{"blank_lines_between", "hello\n\n\nworld\n", 2},
		{"whitespace_only_lines", "hello\n   \n\t\nworld", 2},
		{"three_lines", "a\nb\nc", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitLines(tt.in)
			if len(got) != tt.want {
				t.Errorf("splitLines(%q) returned %d lines, want %d; got %v", tt.in, len(got), tt.want, got)
			}
		})
	}
}

func TestSplitLinesContent(t *testing.T) {
	got := splitLines("alpha\nbeta\ngamma\n")
	if len(got) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(got))
	}
	if got[0] != "alpha" || got[1] != "beta" || got[2] != "gamma" {
		t.Errorf("unexpected content: %v", got)
	}
}

func TestParsePoolStatuses_Empty(t *testing.T) {
	pools := parsePoolStatuses("")
	if len(pools) != 0 {
		t.Errorf("expected 0 pools for empty input, got %d", len(pools))
	}
}

func TestParsePoolStatuses_SingleHealthyPool(t *testing.T) {
	input := `  pool: tank
 state: ONLINE
  scan: scrub repaired 0B in 00:01:23 with 0 errors on Sun Mar 15 00:24:00 2026
config:

	NAME        STATE     READ WRITE CKSUM
	tank        ONLINE       0     0     0
	  mirror-0  ONLINE       0     0     0
	    sda     ONLINE       0     0     0
	    sdb     ONLINE       0     0     0

errors: No known data errors
`
	pools := parsePoolStatuses(input)
	if len(pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(pools))
	}

	p := pools[0]
	t.Run("name", func(t *testing.T) {
		if p.Name != "tank" {
			t.Errorf("Name = %q, want %q", p.Name, "tank")
		}
	})
	t.Run("state", func(t *testing.T) {
		if p.State != "ONLINE" {
			t.Errorf("State = %q, want %q", p.State, "ONLINE")
		}
	})
	t.Run("scan", func(t *testing.T) {
		want := "scrub repaired 0B in 00:01:23 with 0 errors on Sun Mar 15 00:24:00 2026"
		if p.Scan != want {
			t.Errorf("Scan = %q, want %q", p.Scan, want)
		}
	})
	t.Run("errors", func(t *testing.T) {
		if p.Errors != "No known data errors" {
			t.Errorf("Errors = %q, want %q", p.Errors, "No known data errors")
		}
	})
	t.Run("status_empty", func(t *testing.T) {
		if p.Status != "" {
			t.Errorf("Status = %q, want empty", p.Status)
		}
	})
	t.Run("vdev_count", func(t *testing.T) {
		if len(p.Vdevs) != 4 {
			t.Fatalf("expected 4 vdevs, got %d: %+v", len(p.Vdevs), p.Vdevs)
		}
	})
	t.Run("vdev_root", func(t *testing.T) {
		v := p.Vdevs[0]
		if v.Name != "tank" || v.State != "ONLINE" || v.Depth != 0 {
			t.Errorf("root vdev = %+v", v)
		}
	})
	t.Run("vdev_mirror", func(t *testing.T) {
		v := p.Vdevs[1]
		if v.Name != "mirror-0" || v.State != "ONLINE" || v.Depth != 1 {
			t.Errorf("mirror vdev = %+v", v)
		}
	})
	t.Run("vdev_disk", func(t *testing.T) {
		v := p.Vdevs[2]
		if v.Name != "sda" || v.State != "ONLINE" || v.Depth != 2 {
			t.Errorf("disk vdev = %+v", v)
		}
	})
}

func TestParsePoolStatuses_DegradedWithErrors(t *testing.T) {
	input := `  pool: rpool
 state: DEGRADED
status: One or more devices has been removed by the administrator.
  scan: scrub repaired 0B in 00:05:00 with 0 errors on Sun Mar 15 00:24:00 2026
config:

	NAME        STATE     READ WRITE CKSUM
	rpool       DEGRADED     0     0     0
	  mirror-0  DEGRADED     0     0     0
	    sda1    ONLINE       0     0     0
	    sdb1    REMOVED      3     5     1

errors: 12 data errors, use '-v' for a list
`
	pools := parsePoolStatuses(input)
	if len(pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(pools))
	}

	p := pools[0]
	t.Run("state", func(t *testing.T) {
		if p.State != "DEGRADED" {
			t.Errorf("State = %q, want %q", p.State, "DEGRADED")
		}
	})
	t.Run("status", func(t *testing.T) {
		want := "One or more devices has been removed by the administrator."
		if p.Status != want {
			t.Errorf("Status = %q, want %q", p.Status, want)
		}
	})
	t.Run("errors", func(t *testing.T) {
		want := "12 data errors, use '-v' for a list"
		if p.Errors != want {
			t.Errorf("Errors = %q, want %q", p.Errors, want)
		}
	})
	t.Run("removed_disk_errors", func(t *testing.T) {
		if len(p.Vdevs) < 4 {
			t.Fatalf("expected 4 vdevs, got %d", len(p.Vdevs))
		}
		v := p.Vdevs[3] // sdb1
		if v.Name != "sdb1" {
			t.Errorf("Name = %q, want sdb1", v.Name)
		}
		if v.State != "REMOVED" {
			t.Errorf("State = %q, want REMOVED", v.State)
		}
		if v.Read != 3 {
			t.Errorf("Read = %d, want 3", v.Read)
		}
		if v.Write != 5 {
			t.Errorf("Write = %d, want 5", v.Write)
		}
		if v.Cksum != 1 {
			t.Errorf("Cksum = %d, want 1", v.Cksum)
		}
	})
}

func TestParsePoolStatuses_MultiplePools(t *testing.T) {
	input := `  pool: tank
 state: ONLINE
  scan: none requested
config:

	NAME        STATE     READ WRITE CKSUM
	tank        ONLINE       0     0     0
	  sda       ONLINE       0     0     0

errors: No known data errors

  pool: backup
 state: ONLINE
  scan: scrub in progress since Mon Mar 16 01:00:00 2026
config:

	NAME        STATE     READ WRITE CKSUM
	backup      ONLINE       0     0     0
	  sdc       ONLINE       0     0     0

errors: No known data errors
`
	pools := parsePoolStatuses(input)
	if len(pools) != 2 {
		t.Fatalf("expected 2 pools, got %d", len(pools))
	}

	t.Run("first_pool_name", func(t *testing.T) {
		if pools[0].Name != "tank" {
			t.Errorf("first pool Name = %q, want %q", pools[0].Name, "tank")
		}
	})
	t.Run("second_pool_name", func(t *testing.T) {
		if pools[1].Name != "backup" {
			t.Errorf("second pool Name = %q, want %q", pools[1].Name, "backup")
		}
	})
	t.Run("first_pool_vdevs", func(t *testing.T) {
		if len(pools[0].Vdevs) != 2 {
			t.Errorf("first pool has %d vdevs, want 2", len(pools[0].Vdevs))
		}
	})
	t.Run("second_pool_vdevs", func(t *testing.T) {
		if len(pools[1].Vdevs) != 2 {
			t.Errorf("second pool has %d vdevs, want 2", len(pools[1].Vdevs))
		}
	})
	t.Run("first_pool_scan", func(t *testing.T) {
		if pools[0].Scan != "none requested" {
			t.Errorf("first pool Scan = %q, want %q", pools[0].Scan, "none requested")
		}
	})
}

func TestParsePoolStatuses_ScrubInProgress_MultiLineScan(t *testing.T) {
	input := `  pool: tank
 state: ONLINE
  scan: scrub in progress since Mon Mar 16 01:00:00 2026
	1.23T scanned at 456M/s, 789G issued at 123M/s, 2.00T total
	0B repaired, 38.67% done, 03:45:12 to go
config:

	NAME        STATE     READ WRITE CKSUM
	tank        ONLINE       0     0     0
	  raidz1-0  ONLINE       0     0     0
	    sda     ONLINE       0     0     0
	    sdb     ONLINE       0     0     0
	    sdc     ONLINE       0     0     0

errors: No known data errors
`
	pools := parsePoolStatuses(input)
	if len(pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(pools))
	}

	p := pools[0]
	t.Run("scan_multiline", func(t *testing.T) {
		wantPrefix := "scrub in progress since Mon Mar 16 01:00:00 2026"
		if len(p.Scan) < len(wantPrefix) || p.Scan[:len(wantPrefix)] != wantPrefix {
			t.Errorf("Scan does not start with expected prefix.\nGot:  %q", p.Scan)
		}
		// The continuation lines should be joined with newlines.
		if !contains(p.Scan, "38.67% done") {
			t.Errorf("Scan missing continuation data.\nGot: %q", p.Scan)
		}
		if !contains(p.Scan, "1.23T scanned") {
			t.Errorf("Scan missing first continuation line.\nGot: %q", p.Scan)
		}
	})
	t.Run("vdev_count", func(t *testing.T) {
		if len(p.Vdevs) != 5 {
			t.Fatalf("expected 5 vdevs, got %d: %+v", len(p.Vdevs), p.Vdevs)
		}
	})
	t.Run("raidz_depth", func(t *testing.T) {
		v := p.Vdevs[1]
		if v.Name != "raidz1-0" || v.Depth != 1 {
			t.Errorf("raidz vdev = %+v, want Name=raidz1-0 Depth=1", v)
		}
	})
	t.Run("disk_depth", func(t *testing.T) {
		v := p.Vdevs[2]
		if v.Name != "sda" || v.Depth != 2 {
			t.Errorf("disk vdev = %+v, want Name=sda Depth=2", v)
		}
	})
}

func TestParsePoolStatuses_NoVdevs(t *testing.T) {
	// Minimal pool output without a config section (unusual but should not crash).
	input := `  pool: orphan
 state: FAULTED
  scan: none requested
errors: pool I/O is currently suspended
`
	pools := parsePoolStatuses(input)
	if len(pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(pools))
	}
	if pools[0].Name != "orphan" {
		t.Errorf("Name = %q, want orphan", pools[0].Name)
	}
	if pools[0].State != "FAULTED" {
		t.Errorf("State = %q, want FAULTED", pools[0].State)
	}
	if len(pools[0].Vdevs) != 0 {
		t.Errorf("expected 0 vdevs, got %d", len(pools[0].Vdevs))
	}
}

func TestParsePoolStatuses_VdevErrorCounts(t *testing.T) {
	input := `  pool: tank
 state: ONLINE
  scan: none requested
config:

	NAME        STATE     READ WRITE CKSUM
	tank        ONLINE     100   200   300

errors: No known data errors
`
	pools := parsePoolStatuses(input)
	if len(pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(pools))
	}
	if len(pools[0].Vdevs) != 1 {
		t.Fatalf("expected 1 vdev, got %d", len(pools[0].Vdevs))
	}
	v := pools[0].Vdevs[0]
	if v.Read != 100 {
		t.Errorf("Read = %d, want 100", v.Read)
	}
	if v.Write != 200 {
		t.Errorf("Write = %d, want 200", v.Write)
	}
	if v.Cksum != 300 {
		t.Errorf("Cksum = %d, want 300", v.Cksum)
	}
}

// contains is a test helper for substring matching.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
