package schedule

import "time"

// nextDaily returns the next local hour:minute strictly after after. Each day is
// resolved through wallTime, so a skipped DST day rolls to the next one instead
// of firing at the wrong instant.
func (s *Schedule) nextDaily(after time.Time) (time.Time, bool) {
	a := after.In(s.loc)
	for d := 0; d < 3; d++ {
		day := a.AddDate(0, 0, d)
		t, ok := wallTime(day.Year(), day.Month(), day.Day(), s.spec.Hour, s.spec.Minute, 0, s.loc)
		if !ok {
			continue // the local time does not exist that day: skip it
		}
		if t.After(after) {
			return t, true
		}
	}
	return time.Time{}, false
}

// nextWeekly is nextDaily over the chosen weekdays. 14 days cover every
// combination including a skipped occurrence.
func (s *Schedule) nextWeekly(after time.Time) (time.Time, bool) {
	a := after.In(s.loc)
	for d := 0; d < 15; d++ {
		day := a.AddDate(0, 0, d)
		if !weekdayIn(day.Weekday(), s.spec.Weekdays) {
			continue
		}
		t, ok := wallTime(day.Year(), day.Month(), day.Day(), s.spec.Hour, s.spec.Minute, 0, s.loc)
		if !ok {
			continue
		}
		if t.After(after) {
			return t, true
		}
	}
	return time.Time{}, false
}

func weekdayIn(d time.Weekday, days []time.Weekday) bool {
	for _, want := range days {
		if want == d {
			return true
		}
	}
	return false
}

// wallTime resolves the local wall clock (y,m,d,h,mi,s) in loc.
//
// The scheduler's rule is the written one, not Go's default: a wall time that does
// not exist (the spring-forward gap) is skipped, and a repeated one (the fall-back
// overlap) resolves to the earlier instant. time.Date already normalizes a
// nonexistent wall time to a different clock reading, so a wall-clock round trip
// detects the gap exactly; the only thing it does not promise is which side of an
// ambiguous time it picks ("does not guarantee which" in the time.Date docs), so
// the repeated case is resolved explicitly by probing the other side of the
// transition and keeping the earlier instant.
func wallTime(y int, mo time.Month, d, h, mi, sec int, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = time.Local
	}
	t := time.Date(y, mo, d, h, mi, sec, 0, loc)
	if !sameWall(t, y, mo, d, h, mi, sec) {
		return time.Time{}, false // the spring-forward gap: this occurrence does not exist
	}
	// A repeated wall time has two instants. Their distance is the DST delta of
	// this zone (30, 45, 60 or 120 minutes in every zone the tz database has
	// today), so probing those deltas on both sides finds the twin without
	// enumerating zone offsets.
	best := t
	for _, delta := range [...]time.Duration{
		-30 * time.Minute, 30 * time.Minute,
		-time.Hour, time.Hour,
		-45 * time.Minute, 45 * time.Minute,
		-2 * time.Hour, 2 * time.Hour,
	} {
		if alt := t.Add(delta); sameWall(alt, y, mo, d, h, mi, sec) && alt.Before(best) {
			best = alt
		}
	}
	return best, true
}

// sameWall reports whether t reads back as the requested wall clock in its own
// location. time.Date normalizes out-of-range values, so a mismatch means the
// requested wall time does not exist there.
func sameWall(t time.Time, y int, mo time.Month, d, h, mi, sec int) bool {
	ty, tmo, td := t.Date()
	th, tmi, ts := t.Clock()
	return ty == y && tmo == mo && td == d && th == h && tmi == mi && ts == sec
}
