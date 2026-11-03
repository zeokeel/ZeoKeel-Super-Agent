package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronExpr is a parsed five-field expression. Every field is a boolean table so
// matching one local day is a few array reads, which keeps Next cheap during the
// runtime's poll loop.
type cronExpr struct {
	minutes, hours []bool
	doms, months   []bool
	dows           []bool
	domRestricted  bool
	dowRestricted  bool
}

// parseCron parses "minute hour day-of-month month day-of-week".
//
// Day-of-month and day-of-week follow Vixie cron: when both are restricted a
// day matches if either matches; when only one is restricted that one decides.
// A field starting with '*' is unrestricted even with a step, as in cronie.
//
// This parser is deliberately not delegated to robfig/cron/v3 (which is pure Go
// and available): robfig clears its "star" bit when a step is present, so
// "0 0 */2 * 1" would flip DOM/DOW from AND to OR, it treats "N/step" differently
// in past versions, and its DST handling is weaker than the wall-clock rule this
// package promises (next.go). Swapping it would change scheduling semantics to buy
// 120 lines; the decision is to keep the parser and test it instead.
func parseCron(expr string) (*cronExpr, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("schedule: cron needs 5 fields, got %d", len(fields))
	}
	var e cronExpr
	var err error
	if e.minutes, _, err = parseCronField(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("schedule: cron minute: %w", err)
	}
	if e.hours, _, err = parseCronField(fields[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("schedule: cron hour: %w", err)
	}
	if e.doms, e.domRestricted, err = parseCronField(fields[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("schedule: cron day-of-month: %w", err)
	}
	if e.months, _, err = parseCronField(fields[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("schedule: cron month: %w", err)
	}
	if e.dows, e.dowRestricted, err = parseCronField(fields[4], 0, 7, weekdayNames); err != nil {
		return nil, fmt.Errorf("schedule: cron day-of-week: %w", err)
	}
	// 7 and 0 are both Sunday; normalise so the table has one Sunday entry.
	if len(e.dows) > 7 && e.dows[7] {
		e.dows[0] = true
	}
	return &e, nil
}

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var weekdayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// parseCronField expands one field into its value table and reports whether the
// field is restricted (does not start with '*').
func parseCronField(raw string, lo, hi int, names map[string]int) ([]bool, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false, fmt.Errorf("empty field")
	}
	set := make([]bool, hi+1)
	restricted := !strings.HasPrefix(raw, "*")
	for _, elem := range strings.Split(raw, ",") {
		elem = strings.TrimSpace(elem)
		if elem == "" {
			return nil, false, fmt.Errorf("empty list item")
		}
		base, step := elem, 1
		hasStep := false
		if i := strings.IndexByte(elem, '/'); i >= 0 {
			base = elem[:i]
			n, err := strconv.Atoi(elem[i+1:])
			if err != nil || n <= 0 {
				return nil, false, fmt.Errorf("bad step %q", elem)
			}
			step = n
			hasStep = true
		}
		start, end := lo, hi
		switch {
		case base == "*":
			// the whole range
		case strings.Contains(base, "-"):
			parts := strings.SplitN(base, "-", 2)
			a, err := parseCronValue(parts[0], names)
			if err != nil {
				return nil, false, err
			}
			b, err := parseCronValue(parts[1], names)
			if err != nil {
				return nil, false, err
			}
			start, end = a, b
			if start > end {
				return nil, false, fmt.Errorf("range %q is reversed", base)
			}
		default:
			v, err := parseCronValue(base, names)
			if err != nil {
				return nil, false, err
			}
			start, end = v, v
			// "N/step" means "N-max/step", as cronie and the canonical cron
			// implementations read it: "0/6" in the hour field is every six hours
			// starting at midnight, not only midnight. Reading it as one value
			// silently under-schedules, which is the worst kind of scheduling bug.
			if hasStep {
				end = hi
			}
		}
		if start < lo || end > hi {
			return nil, false, fmt.Errorf("value %q out of range %d-%d", elem, lo, hi)
		}
		for v := start; v <= end; v += step {
			set[v] = true
		}
	}
	any := false
	for _, ok := range set {
		any = any || ok
	}
	if !any {
		return nil, false, fmt.Errorf("field matches nothing")
	}
	return set, restricted, nil
}

func parseCronValue(s string, names map[string]int) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if names != nil {
		if v, ok := names[s]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad value %q", s)
	}
	return v, nil
}

// next scans local days from after and returns the first matching wall time that
// exists and is strictly later. A missing time is skipped, not shifted.
func (e *cronExpr) next(after time.Time, loc *time.Location) (time.Time, bool) {
	start := after.In(loc)
	const maxDays = 366 * 5 // five years: every valid expression has an occurrence by then
	for d := 0; d < maxDays; d++ {
		day := time.Date(start.Year(), start.Month(), start.Day()+d, 0, 0, 0, 0, loc)
		if !e.dayMatches(day) {
			continue
		}
		for h := 0; h < 24; h++ {
			if !e.hours[h] {
				continue
			}
			for m := 0; m < 60; m++ {
				if !e.minutes[m] {
					continue
				}
				t, ok := wallTime(day.Year(), day.Month(), day.Day(), h, m, 0, loc)
				if !ok {
					continue // the spring-forward gap: this occurrence does not exist
				}
				if t.After(after) {
					return t, true
				}
			}
		}
	}
	return time.Time{}, false
}

func (e *cronExpr) dayMatches(day time.Time) bool {
	if !e.months[int(day.Month())] {
		return false
	}
	dom := e.doms[day.Day()]
	dow := e.dows[int(day.Weekday())]
	switch {
	case e.domRestricted && e.dowRestricted:
		return dom || dow
	case e.domRestricted:
		return dom
	case e.dowRestricted:
		return dow
	default:
		return true
	}
}
