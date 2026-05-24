// Package scheduler provides a minimal 5-field cron parser and a ticker-driven
// runner for periodic in-process tasks (replication, future auto-snapshot, etc).
//
// Granularity is one minute — matching the precision exposed by TrueNAS and
// other ZFS appliances. No catch-up on missed firings: if the host was asleep
// or the service was down, scheduled times that have already passed are not
// run after the fact. Tasks that need durable scheduling guarantees should
// persist their own run history.
package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed cron expression of the form `minute hour dom month dow`.
// dow uses 0 = Sunday, 6 = Saturday. dow=7 is accepted as an alias for Sunday.
//
// When both dom and dow are restricted (neither is `*`), Matches returns true
// if either field matches the given time — this is the standard Vixie cron
// behaviour and what TrueNAS users will expect.
type Schedule struct {
	minute   [60]bool
	hour     [24]bool
	dom      [32]bool // index 1..31 used
	month    [13]bool // index 1..12 used
	dow      [7]bool  // index 0..6 used (0 = Sunday)
	domStar  bool     // true when dom field was "*"
	dowStar  bool     // true when dow field was "*"
	original string
}

// String returns the original cron expression Parse was called with.
func (s Schedule) String() string { return s.original }

// Matches reports whether t falls on a minute the schedule fires.
// Seconds are ignored; callers driving Matches from a ticker should align
// the ticker to wall-clock minute boundaries.
func (s Schedule) Matches(t time.Time) bool {
	if !s.minute[t.Minute()] || !s.hour[t.Hour()] || !s.month[int(t.Month())] {
		return false
	}
	domOK := s.dom[t.Day()]
	dowOK := s.dow[int(t.Weekday())]
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return dowOK
	case s.dowStar:
		return domOK
	default:
		// Vixie semantics: either matches.
		return domOK || dowOK
	}
}

// Parse parses a 5-field cron expression.
//
// Each field supports:
//   - "*"          — all valid values
//   - "N"          — a single value
//   - "N-M"        — a closed range
//   - "*/S"        — every S starting at the field's minimum
//   - "N-M/S"      — every S inside a range
//   - "a,b,c"      — a comma-separated list of any of the above
//
// Day-of-week accepts 0-6 (0 = Sunday); 7 is also accepted as Sunday for
// compatibility. Symbolic names (MON, JAN, …) and `@hourly` aliases are
// deliberately not supported — keep the surface small.
func Parse(spec string) (Schedule, error) {
	spec = strings.TrimSpace(spec)
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("expected 5 fields, got %d", len(fields))
	}

	s := Schedule{original: spec}

	if err := parseField(fields[0], 0, 59, s.minute[:]); err != nil {
		return Schedule{}, fmt.Errorf("minute: %w", err)
	}
	if err := parseField(fields[1], 0, 23, s.hour[:]); err != nil {
		return Schedule{}, fmt.Errorf("hour: %w", err)
	}
	if err := parseField(fields[2], 1, 31, s.dom[:]); err != nil {
		return Schedule{}, fmt.Errorf("day-of-month: %w", err)
	}
	if err := parseField(fields[3], 1, 12, s.month[:]); err != nil {
		return Schedule{}, fmt.Errorf("month: %w", err)
	}

	// dow accepts 7 as an alias for 0; normalise before storing.
	dowSpec := strings.ReplaceAll(fields[4], "7", "0")
	if err := parseField(dowSpec, 0, 6, s.dow[:]); err != nil {
		return Schedule{}, fmt.Errorf("day-of-week: %w", err)
	}

	s.domStar = fields[2] == "*"
	s.dowStar = fields[4] == "*"
	return s, nil
}

// parseField fills out (a slice large enough to index up to max) with true
// for each value the field matches. min/max are the inclusive valid range.
func parseField(field string, min, max int, out []bool) error {
	if field == "" {
		return fmt.Errorf("empty")
	}
	for _, part := range strings.Split(field, ",") {
		if err := parsePart(part, min, max, out); err != nil {
			return err
		}
	}
	return nil
}

func parsePart(part string, min, max int, out []bool) error {
	step := 1
	if idx := strings.Index(part, "/"); idx >= 0 {
		s, err := strconv.Atoi(part[idx+1:])
		if err != nil || s <= 0 {
			return fmt.Errorf("invalid step %q", part[idx+1:])
		}
		step = s
		part = part[:idx]
	}

	var lo, hi int
	switch {
	case part == "*":
		lo, hi = min, max
	case strings.Contains(part, "-"):
		bits := strings.SplitN(part, "-", 2)
		l, err := strconv.Atoi(bits[0])
		if err != nil {
			return fmt.Errorf("invalid range start %q", bits[0])
		}
		h, err := strconv.Atoi(bits[1])
		if err != nil {
			return fmt.Errorf("invalid range end %q", bits[1])
		}
		lo, hi = l, h
	default:
		n, err := strconv.Atoi(part)
		if err != nil {
			return fmt.Errorf("invalid value %q", part)
		}
		// A bare number with /S is treated as N..max (matches Vixie cron).
		lo = n
		hi = n
		if step > 1 {
			hi = max
		}
	}

	if lo < min || hi > max || lo > hi {
		return fmt.Errorf("value %d-%d out of range %d-%d", lo, hi, min, max)
	}
	for v := lo; v <= hi; v += step {
		out[v] = true
	}
	return nil
}
