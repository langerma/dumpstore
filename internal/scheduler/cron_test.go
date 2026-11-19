package scheduler

import (
	"testing"
	"time"
)

func TestParseErrors(t *testing.T) {
	cases := []string{
		"",
		"* * * *",         // four fields
		"* * * * * *",     // six fields
		"60 * * * *",      // minute out of range
		"* 24 * * *",      // hour out of range
		"* * 0 * *",       // dom out of range (min is 1)
		"* * 32 * *",      // dom out of range
		"* * * 13 *",      // month out of range
		"* * * * 8",       // dow out of range (after 7→0 normalisation, still 8 unchanged)
		"abc * * * *",     // not a number
		"*/0 * * * *",     // zero step
		"5-3 * * * *",     // inverted range
		"*/-1 * * * *",    // negative step
	}
	for _, spec := range cases {
		if _, err := Parse(spec); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", spec)
		}
	}
}

func TestParseValid(t *testing.T) {
	cases := []string{
		"* * * * *",
		"0 * * * *",
		"*/5 * * * *",
		"0 0 * * *",
		"15,45 9-17 * * 1-5",
		"0 0 1 1 *",
		"0 0 * * 7", // Sunday alias
	}
	for _, spec := range cases {
		if _, err := Parse(spec); err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", spec, err)
		}
	}
}

func TestMatches(t *testing.T) {
	must := func(spec string) Schedule {
		t.Helper()
		s, err := Parse(spec)
		if err != nil {
			t.Fatalf("Parse(%q): %v", spec, err)
		}
		return s
	}
	at := func(s string) time.Time {
		t.Helper()
		tt, err := time.Parse("2006-01-02 15:04 MST", s)
		if err != nil {
			t.Fatalf("time.Parse(%q): %v", s, err)
		}
		return tt
	}

	cases := []struct {
		spec string
		when string
		want bool
	}{
		{"* * * * *", "2026-05-24 10:30 UTC", true},
		{"0 * * * *", "2026-05-24 10:00 UTC", true},
		{"0 * * * *", "2026-05-24 10:01 UTC", false},
		{"*/5 * * * *", "2026-05-24 10:15 UTC", true},
		{"*/5 * * * *", "2026-05-24 10:16 UTC", false},
		{"15,45 * * * *", "2026-05-24 10:15 UTC", true},
		{"15,45 * * * *", "2026-05-24 10:30 UTC", false},
		{"0 9-17 * * 1-5", "2026-05-25 09:00 UTC", true},  // Mon
		{"0 9-17 * * 1-5", "2026-05-24 09:00 UTC", false}, // Sun
		{"0 9-17 * * 1-5", "2026-05-25 08:00 UTC", false}, // before window
		// Vixie semantics: if dom and dow both restricted, either matches.
		{"0 0 1 * 0", "2026-05-24 00:00 UTC", true},  // Sunday, not the 1st — dow matches
		{"0 0 1 * 0", "2026-06-01 00:00 UTC", true},  // Monday, but the 1st — dom matches
		{"0 0 1 * 0", "2026-05-22 00:00 UTC", false}, // neither matches
		{"0 0 * * 7", "2026-05-24 00:00 UTC", true},  // 7→0 alias for Sunday
		// Range 0-7 covers every day of the week (0=Sunday, 7=Sunday alias).
		{"0 0 * * 0-7", "2026-05-24 00:00 UTC", true}, // Sun
		{"0 0 * * 0-7", "2026-05-25 00:00 UTC", true}, // Mon
		{"0 0 * * 0-7", "2026-05-26 00:00 UTC", true}, // Tue
		{"0 0 * * 0-7", "2026-05-27 00:00 UTC", true}, // Wed
		{"0 0 * * 0-7", "2026-05-28 00:00 UTC", true}, // Thu
		{"0 0 * * 0-7", "2026-05-29 00:00 UTC", true}, // Fri
		{"0 0 * * 0-7", "2026-05-30 00:00 UTC", true}, // Sat
		// Bare 7 expands to both Sunday and Saturday.
		{"0 0 * * 7", "2026-05-24 00:00 UTC", true},  // Sunday
		{"0 0 * * 7", "2026-05-30 00:00 UTC", true},  // Saturday
		// Mixed: 1-5,7 expands to Mon-Fri + Sun(0) + Sat(6) = every day.
		{"0 0 * * 1-5,7", "2026-05-24 00:00 UTC", true}, // Sun
		{"0 0 * * 1-5,7", "2026-05-25 00:00 UTC", true}, // Mon
		{"0 0 * * 1-5,7", "2026-05-29 00:00 UTC", true}, // Fri
		{"0 0 * * 1-5,7", "2026-05-30 00:00 UTC", true}, // Sat
	}
	for _, c := range cases {
		s := must(c.spec)
		got := s.Matches(at(c.when))
		if got != c.want {
			t.Errorf("Matches(%q at %q) = %v, want %v", c.spec, c.when, got, c.want)
		}
	}
}
