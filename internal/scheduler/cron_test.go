package scheduler

import (
	"fmt"
	"testing"
	"time"
)

func TestParseErrors(t *testing.T) {
	cases := []string{
		"",
		"* * * *",      // four fields
		"* * * * * *",  // six fields
		"60 * * * *",   // minute out of range
		"* 24 * * *",   // hour out of range
		"* * 0 * *",    // dom out of range (min is 1)
		"* * 32 * *",   // dom out of range
		"* * * 13 *",   // month out of range
		"* * * * 8",    // dow out of range (7 is the highest, an alias for Sunday)
		"* * * * 0-8",  // dow range past 7
		"* * * * 7-1",  // inverted dow range
		"abc * * * *",  // not a number
		"*/0 * * * *",  // zero step
		"5-3 * * * *",  // inverted range
		"*/-1 * * * *", // negative step
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
		// A day field starting with "*" selects the AND rule (#225, Vixie):
		// every Monday that is also an odd day of the month.
		{"0 0 */2 * 1", "2026-05-25 00:00 UTC", true},  // Mon 25th (odd)
		{"0 0 */2 * 1", "2026-06-01 00:00 UTC", true},  // Mon 1st (odd)
		{"0 0 */2 * 1", "2026-06-08 00:00 UTC", false}, // Mon 8th (even)
		{"0 0 */2 * 1", "2026-05-27 00:00 UTC", false}, // Wed 27th (odd, not Monday)
		{"0 0 1 * */2", "2026-06-02 00:00 UTC", false}, // Tue 2nd: neither
		{"0 0 1 * */2", "2026-05-31 00:00 UTC", false}, // Sun 31st: dow ok, not the 1st
		{"0 0 1 * */2", "2026-06-01 00:00 UTC", false}, // Mon 1st: the 1st, but Monday (1) isn't even
		{"0 0 1 * */2", "2026-11-01 00:00 UTC", true},  // Sun 1st: both
	}
	for _, c := range cases {
		s := must(c.spec)
		got := s.Matches(at(c.when))
		if got != c.want {
			t.Errorf("Matches(%q at %q) = %v, want %v", c.spec, c.when, got, c.want)
		}
	}
}

// TestDayOfWeekSeven pins #225: 7 is Sunday and nothing else, and ranges,
// lists and steps that reach 7 keep their Sunday. Each spec lists the
// weekdays (0 = Sunday) it must fire on during one week.
func TestDayOfWeekSeven(t *testing.T) {
	sunday := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		dow  string
		want []int
	}{
		{"7", []int{0}},
		{"0", []int{0}},
		{"0-7", []int{0, 1, 2, 3, 4, 5, 6}},
		{"1-7", []int{0, 1, 2, 3, 4, 5, 6}},
		{"5-7", []int{0, 5, 6}},
		{"6-7", []int{0, 6}},
		{"1-5,7", []int{0, 1, 2, 3, 4, 5}},
		{"1-7/2", []int{0, 1, 3, 5}}, // 1,3,5,7
		{"*/2", []int{0, 2, 4, 6}},
		{"*", []int{0, 1, 2, 3, 4, 5, 6}},
	} {
		s, err := Parse("0 0 * * " + c.dow)
		if err != nil {
			t.Errorf("dow %q: %v", c.dow, err)
			continue
		}
		var got []int
		for d := 0; d < 7; d++ {
			if s.Matches(sunday.AddDate(0, 0, d)) {
				got = append(got, d)
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("dow %q fires on %v, want %v", c.dow, got, c.want)
		}
	}
}
