package schedule_test

// verifier-A: independent falsification of the Wave-1 schedule claims
// (audit N-12: cron "N/step" semantics and DST/wall-clock handling).
// Independent of the owner's tests: the cases below are written from the cron
// and tz definitions, not from the implementation's helpers.

import (
	"testing"
	"time"
	_ "time/tzdata" // keep the test independent of the host's zoneinfo

	"github.com/zeokeel/super-agent/internal/schedule"
)

func nextAt(t *testing.T, spec schedule.Spec, after time.Time) time.Time {
	t.Helper()
	s, err := schedule.Compile(spec)
	if err != nil {
		t.Fatalf("Compile(%+v): %v", spec, err)
	}
	got, ok := s.Next(after)
	if !ok {
		t.Fatalf("Next(%+v, %s) = no occurrence", spec, after.Format(time.RFC3339))
	}
	return got.UTC()
}

func mustUTC(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad test time %q: %v", s, err)
	}
	return tt
}

// "N/step" means N-max/step (cronie): 0/6 in the hour field is every six hours
// starting at midnight, not only midnight. The old parser read it as one value.
func TestVerifyCronNumericStartStep(t *testing.T) {
	spec := schedule.Spec{Kind: schedule.Cron, Cron: "0 0/6 * * *", Location: "UTC"}
	after := mustUTC(t, "2026-01-01T00:30:00Z")
	want := []string{"2026-01-01T06:00:00Z", "2026-01-01T12:00:00Z", "2026-01-01T18:00:00Z", "2026-01-02T00:00:00Z"}
	for _, w := range want {
		after = nextAt(t, spec, after)
		if got := after.Format(time.RFC3339); got != w {
			t.Fatalf("0/6 hour: next = %s, want %s", got, w)
		}
	}

	// 5/10 minutes: 5,15,25,35,45,55 of every hour.
	spec = schedule.Spec{Kind: schedule.Cron, Cron: "5/10 * * * *", Location: "UTC"}
	after = mustUTC(t, "2026-01-01T00:00:00Z")
	want = []string{
		"2026-01-01T00:05:00Z", "2026-01-01T00:15:00Z", "2026-01-01T00:25:00Z",
		"2026-01-01T00:35:00Z", "2026-01-01T00:45:00Z", "2026-01-01T00:55:00Z",
		"2026-01-01T01:05:00Z",
	}
	for _, w := range want {
		after = nextAt(t, spec, after)
		if got := after.Format(time.RFC3339); got != w {
			t.Fatalf("5/10 minute: next = %s, want %s", got, w)
		}
	}

	// */15 is unchanged: 0,15,30,45.
	spec = schedule.Spec{Kind: schedule.Cron, Cron: "*/15 * * * *", Location: "UTC"}
	after = mustUTC(t, "2026-01-01T00:01:00Z")
	want = []string{"2026-01-01T00:15:00Z", "2026-01-01T00:30:00Z", "2026-01-01T00:45:00Z", "2026-01-01T01:00:00Z"}
	for _, w := range want {
		after = nextAt(t, spec, after)
		if got := after.Format(time.RFC3339); got != w {
			t.Fatalf("*/15: next = %s, want %s", got, w)
		}
	}
}

// The spring-forward gap (US: 2026-03-08 02:00 EST -> 03:00 EDT): a 02:30 daily
// wall time does not exist that day and must be skipped, not fired at 01:30/03:30.
func TestVerifyDSTSpringGapSkipped(t *testing.T) {
	spec := schedule.Spec{Kind: schedule.Daily, Hour: 2, Minute: 30, Location: "America/New_York"}

	// The day before the gap: the gap day is skipped entirely.
	got := nextAt(t, spec, mustUTC(t, "2026-03-07T12:00:00Z"))
	if want := mustUTC(t, "2026-03-09T06:30:00Z"); !got.Equal(want) {
		t.Fatalf("after Mar 7: next = %s, want 2026-03-09T06:30Z (the 02:30 gap on Mar 8 must be skipped)", got.Format(time.RFC3339))
	}
	if got.Day() == 8 {
		t.Fatal("the wall time that does not exist on Mar 8 was fired")
	}

	// From inside the gap day (08:00 EDT): the next is the day after.
	got = nextAt(t, spec, mustUTC(t, "2026-03-08T12:00:00Z"))
	if want := mustUTC(t, "2026-03-09T06:30:00Z"); !got.Equal(want) {
		t.Fatalf("inside gap day: next = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// The fall-back overlap (US: 2026-11-01 02:00 EDT -> 01:00 EST) repeats the
// 01:00-02:00 wall hour, so 01:30 happens twice; the rule is the EARLIER
// instant (05:30Z, EDT), not the later one (06:30Z, EST).
func TestVerifyDSTFallBackPicksEarlier(t *testing.T) {
	spec := schedule.Spec{Kind: schedule.Daily, Hour: 1, Minute: 30, Location: "America/New_York"}
	got := nextAt(t, spec, mustUTC(t, "2026-10-31T12:00:00Z"))
	if want := mustUTC(t, "2026-11-01T05:30:00Z"); !got.Equal(want) {
		t.Fatalf("fall-back next = %s, want the earlier 2026-11-01T05:30Z", got.Format(time.RFC3339))
	}
}

// Lord Howe has a 30-minute DST shift: 2026-10-04 02:00 -> 02:30, so a 02:15
// daily time does not exist that day.
func TestVerifyDSTThirtyMinuteGap(t *testing.T) {
	spec := schedule.Spec{Kind: schedule.Daily, Hour: 2, Minute: 15, Location: "Australia/Lord_Howe"}
	got := nextAt(t, spec, mustUTC(t, "2026-10-03T12:00:00Z"))
	// 2026-10-05 02:15 at UTC+11 = 2026-10-04T15:15Z.
	if want := mustUTC(t, "2026-10-04T15:15:00Z"); !got.Equal(want) {
		t.Fatalf("Lord Howe gap: next = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if got.In(mustLoad(t, "Australia/Lord_Howe")).Day() == 4 {
		t.Fatal("Lord Howe 02:15 fired on the 30-minute spring-forward day")
	}
}

// São Paulo's DST used to start at midnight (2018-11-04 00:00 -> 01:00), the
// historical midnight hole: a 00:00 daily time must skip that day, not fire at
// 23:00 the day before or 01:00 that day.
func TestVerifyDSTMidnightHole(t *testing.T) {
	spec := schedule.Spec{Kind: schedule.Daily, Hour: 0, Minute: 0, Location: "America/Sao_Paulo"}
	got := nextAt(t, spec, mustUTC(t, "2018-11-03T12:00:00Z"))
	loc := mustLoad(t, "America/Sao_Paulo")
	// 2018-11-05 00:00 -02 = 02:00Z.
	want := time.Date(2018, 11, 5, 0, 0, 0, 0, loc).UTC()
	if !got.Equal(want) {
		t.Fatalf("midnight hole: next = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if got.In(loc).Day() == 4 {
		t.Fatal("the midnight hole day was fired")
	}
}

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}
