package schedule

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // embedded zoneinfo: DST tests must not depend on the host's tzdata

	"github.com/zeokeel/super-agent/internal/store"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func compile(t *testing.T, spec Spec) *Schedule {
	t.Helper()
	sch, err := Compile(spec)
	if err != nil {
		t.Fatalf("compile %+v: %v", spec, err)
	}
	return sch
}

func TestCompileRejectsInvalidSpecs(t *testing.T) {
	cases := []Spec{
		{Kind: "nope"},
		{Kind: Once},
		{Kind: Every},
		{Kind: Every, Every: -time.Second},
		{Kind: Daily, Hour: 24, Minute: 0},
		{Kind: Daily, Hour: 9, Minute: 60},
		{Kind: Weekly, Hour: 9, Minute: 0},
		{Kind: Cron, Cron: "* * * *"},
		{Kind: Cron, Cron: "61 * * * *"},
		{Kind: Cron, Cron: "* 24 * * *"},
		{Kind: Cron, Cron: "* * 0 * *"},
		{Kind: Cron, Cron: "* * * 13 *"},
		{Kind: Cron, Cron: "* * * * 8"},
		{Kind: Cron, Cron: "*/0 * * * *"},
		{Kind: Cron, Cron: "5-1 * * * *"},
	}
	for _, spec := range cases {
		if _, err := Compile(spec); err == nil {
			t.Errorf("Compile(%+v) succeeded, want error", spec)
		}
	}
}

func TestNextOnceAndEvery(t *testing.T) {
	base := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	once := compile(t, Spec{Kind: Once, At: base.Add(2 * time.Hour)})
	if got, ok := once.Next(base); !ok || !got.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("once next = %v ok=%v", got, ok)
	}
	if _, ok := once.Next(base.Add(3 * time.Hour)); ok {
		t.Fatal("a one-time schedule fired twice")
	}
	every := compile(t, Spec{Kind: Every, Every: 90 * time.Second})
	if got, ok := every.Next(base); !ok || !got.Equal(base.Add(90*time.Second)) {
		t.Fatalf("every next = %v ok=%v", got, ok)
	}
}

func TestNextDailyAndWeeklyLocalWallClock(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	after := time.Date(2026, 11, 3, 8, 0, 0, 0, loc) // Tuesday
	daily := compile(t, Spec{Kind: Daily, Hour: 9, Minute: 30, Location: loc.String()})
	got, ok := daily.Next(after)
	want := time.Date(2026, 11, 3, 9, 30, 0, 0, loc)
	if !ok || !got.Equal(want) {
		t.Fatalf("daily next = %v, want %v", got, want)
	}
	// After today's time: tomorrow.
	got, ok = daily.Next(want)
	if !ok || !got.Equal(want.Add(24*time.Hour)) {
		t.Fatalf("daily next after fired = %v", got)
	}

	weekly := compile(t, Spec{Kind: Weekly, Hour: 9, Minute: 0,
		Weekdays: []time.Weekday{time.Monday, time.Friday}, Location: loc.String()})
	// Tuesday -> Friday 09:00.
	got, ok = weekly.Next(after)
	want = time.Date(2026, 11, 6, 9, 0, 0, 0, loc)
	if !ok || !got.Equal(want) {
		t.Fatalf("weekly next = %v, want %v", got, want)
	}
}

func TestDailySpringForwardGapIsSkipped(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// 2026-03-08 02:30 does not exist in New York (clocks jump 02:00 -> 03:00).
	// Start after the 7th's own 02:30 so the gap day is the next candidate.
	after := time.Date(2026, 3, 7, 3, 0, 0, 0, ny)
	sch := compile(t, Spec{Kind: Daily, Hour: 2, Minute: 30, Location: ny.String()})
	got, ok := sch.Next(after)
	if !ok {
		t.Fatal("no next occurrence")
	}
	if got.In(ny).Day() == 8 {
		t.Fatalf("fired in the DST gap: %v", got.In(ny))
	}
	want := time.Date(2026, 3, 9, 2, 30, 0, 0, ny) // Monday, now EDT
	if !got.Equal(want) {
		t.Fatalf("next = %v (%v), want %v", got, got.UTC(), want)
	}
}

func TestDailyFallBackTakesEarlierInstant(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	after := time.Date(2026, 10, 31, 12, 0, 0, 0, ny)
	sch := compile(t, Spec{Kind: Daily, Hour: 1, Minute: 30, Location: ny.String()})
	got, ok := sch.Next(after)
	if !ok {
		t.Fatal("no next occurrence")
	}
	// 2026-11-01 01:30 occurs twice: EDT (05:30Z) then EST (06:30Z). Earlier wins.
	if want := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("next = %v, want earlier instant %v", got, want)
	}
	// The repeated instant is not fired again: the next is the following day.
	got, ok = sch.Next(got)
	if !ok {
		t.Fatal("no next after fall-back occurrence")
	}
	if want := time.Date(2026, 11, 2, 6, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("second occurrence = %v, want %v", got, want)
	}
}

func TestCronNextAndOrSemantics(t *testing.T) {
	utc := time.UTC
	after := time.Date(2026, 11, 3, 12, 7, 0, 0, utc) // Tuesday
	// Every 15 minutes: next is 12:15.
	quarter := compile(t, Spec{Kind: Cron, Cron: "*/15 * * * *", Location: "UTC"})
	if got, ok := quarter.Next(after); !ok || !got.Equal(time.Date(2026, 11, 3, 12, 15, 0, 0, utc)) {
		t.Fatalf("*/15 next = %v ok=%v", got, ok)
	}
	// Weekday names and ranges: Mon-Fri 09:00 -> tomorrow 09:00.
	weekdays := compile(t, Spec{Kind: Cron, Cron: "0 9 * * MON-FRI", Location: "UTC"})
	if got, ok := weekdays.Next(after); !ok || !got.Equal(time.Date(2026, 11, 4, 9, 0, 0, 0, utc)) {
		t.Fatalf("MON-FRI next = %v ok=%v", got, ok)
	}
	// Both dom and dow restricted: either matches (Vixie). 2026-11-20 is a
	// Friday, earlier than the 13th of December.
	or := compile(t, Spec{Kind: Cron, Cron: "0 0 13 * FRI", Location: "UTC"})
	if got, ok := or.Next(time.Date(2026, 11, 14, 0, 0, 0, 0, utc)); !ok ||
		!got.Equal(time.Date(2026, 11, 20, 0, 0, 0, 0, utc)) {
		t.Fatalf("dom-or-dow next = %v ok=%v", got, ok)
	}
	// Only day-of-month restricted: the 13th of the next month.
	dom := compile(t, Spec{Kind: Cron, Cron: "0 0 13 * *", Location: "UTC"})
	if got, ok := dom.Next(after); !ok || !got.Equal(time.Date(2026, 11, 13, 0, 0, 0, 0, utc)) {
		t.Fatalf("dom next = %v ok=%v", got, ok)
	}
}

func TestCronSpringForwardSkipsMissingTime(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// Recurring 02:30 in the gap: that day has no such wall time, so the next
	// matching day is the answer.
	sch := compile(t, Spec{Kind: Cron, Cron: "30 2 * * *", Location: ny.String()})
	// After the 7th's occurrence, the 8th's 02:30 is missing, so the next is the 9th.
	after := time.Date(2026, 3, 7, 3, 0, 0, 0, ny)
	got, ok := sch.Next(after)
	if !ok {
		t.Fatal("no next")
	}
	if got.In(ny).Day() != 9 || got.In(ny).Hour() != 2 || got.In(ny).Minute() != 30 {
		t.Fatalf("next = %v, want 2026-03-09 02:30 local", got.In(ny))
	}
}

func TestPutGetListRoundTrip(t *testing.T) {
	st := openStore(t)
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	m, err := New(st, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	def := Definition{ID: "sch-1", Spec: Spec{Kind: Daily, Hour: 9, Minute: 0, Location: "UTC"}, Enabled: true}
	got, err := m.Put(def)
	if err != nil {
		t.Fatal(err)
	}
	if !got.NextAt.Equal(time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("next_at = %v", got.NextAt)
	}
	def2, ok, err := m.Get("sch-1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if def2.Spec.Hour != 9 || !def2.Enabled {
		t.Fatalf("round trip lost fields: %+v", def2)
	}
	list, err := m.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v err=%v", list, err)
	}
	if _, err := m.Put(Definition{ID: "", Spec: Spec{Kind: Daily, Hour: 9}}); err == nil {
		t.Fatal("empty id accepted")
	}
}

func TestClaimSkipsWhilePreviousRunIsRunning(t *testing.T) {
	st := openStore(t)
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	m, _ := New(st, WithClock(func() time.Time { return now }))
	if _, err := m.Put(Definition{ID: "sch-every", Spec: Spec{Kind: Every, Every: time.Minute}, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	first, err := m.ClaimDue(now.Add(2 * time.Minute))
	if err != nil || len(first) != 1 || first[0].Status != "fired" {
		t.Fatalf("first claim = %+v err=%v", first, err)
	}
	// The worker has not completed: the next occurrence is skipped, not queued.
	second, err := m.ClaimDue(now.Add(3 * time.Minute))
	if err != nil || len(second) != 1 || second[0].Status != "skipped" {
		t.Fatalf("overlap claim = %+v err=%v", second, err)
	}
	if !strings.Contains(second[0].Reason, "上一轮仍在运行") {
		t.Fatalf("skip reason = %q", second[0].Reason)
	}
	if err := m.Complete(first[0].FireID); err != nil {
		t.Fatal(err)
	}
	third, err := m.ClaimDue(now.Add(4 * time.Minute))
	if err != nil || len(third) != 1 || third[0].Status != "fired" {
		t.Fatalf("after complete claim = %+v err=%v", third, err)
	}
}

func TestCatchUpOnlyFiresOnceForMissedOccurrences(t *testing.T) {
	st := openStore(t)
	day1 := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	m, _ := New(st, WithClock(func() time.Time { return day1 }))
	// Daily 09:00; next after 08:00 on day1 is day1 09:00.
	if _, err := m.Put(Definition{ID: "sch-daily", Spec: Spec{Kind: Daily, Hour: 9, Minute: 0, Location: "UTC"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// The process was down for four days: exactly one occurrence fires, and the
	// next occurrence is after "now", not a burst of the missed days.
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	claims, err := m.ClaimDue(now)
	if err != nil || len(claims) != 1 || claims[0].Status != "fired" {
		t.Fatalf("catch-up claims = %+v err=%v", claims, err)
	}
	// The first missed occurrence is the planned one; the point is that only one
	// fire happened, not that the latest of the missed days was chosen.
	if want := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC); !claims[0].PlannedAt.Equal(want) {
		t.Fatalf("planned at %v, want the first missed occurrence %v", claims[0].PlannedAt, want)
	}
	again, err := m.ClaimDue(now)
	if err != nil || len(again) != 0 {
		t.Fatalf("second catch-up = %+v err=%v", again, err)
	}
	def, _, _ := m.Get("sch-daily")
	if !def.NextAt.After(now) {
		t.Fatalf("next_at %v is not after now", def.NextAt)
	}
}

func TestRestartDoesNotRepeatAFiredOccurrence(t *testing.T) {
	st := openStore(t)
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	m, _ := New(st, WithClock(func() time.Time { return now }))
	if _, err := m.Put(Definition{ID: "sch-once", Spec: Spec{Kind: Once, At: now.Add(time.Minute)}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if claims, err := m.ClaimDue(now.Add(2 * time.Minute)); err != nil || len(claims) != 1 {
		t.Fatalf("first claim = %+v err=%v", claims, err)
	}
	// A new Manager is a restarted process: the fire row and next_at are on disk,
	// so nothing fires again for the same occurrence.
	m2, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	if claims, err := m2.ClaimDue(now.Add(3 * time.Minute)); err != nil || len(claims) != 0 {
		t.Fatalf("restart claim = %+v err=%v", claims, err)
	}
	fires, err := m2.Fires("sch-once", 10)
	if err != nil || len(fires) != 1 || fires[0].Status != "fired" {
		t.Fatalf("fires after restart = %+v err=%v", fires, err)
	}
}

func TestRecoverSettlesInterruptedFire(t *testing.T) {
	st := openStore(t)
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	m, _ := New(st, WithClock(func() time.Time { return now }))
	if _, err := m.Put(Definition{ID: "sch-every", Spec: Spec{Kind: Every, Every: time.Minute}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if claims, err := m.ClaimDue(now.Add(2 * time.Minute)); err != nil || len(claims) != 1 {
		t.Fatalf("claim = %+v err=%v", claims, err)
	}
	// Simulate a crash: a new process recovers the running fire first.
	m2, _ := New(st)
	n, err := m2.Recover(now.Add(3 * time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("recover = %d err=%v", n, err)
	}
	fires, _ := m2.Fires("sch-every", 10)
	if len(fires) != 1 || fires[0].Status != "interrupted" {
		t.Fatalf("fire status = %+v", fires)
	}
	// The overlap guard is cleared, so the next occurrence can fire.
	claims, err := m2.ClaimDue(now.Add(4 * time.Minute))
	if err != nil || len(claims) != 1 || claims[0].Status != "fired" {
		t.Fatalf("claim after recover = %+v err=%v", claims, err)
	}
}

func TestSetEnabledRecomputesNext(t *testing.T) {
	st := openStore(t)
	now := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	m, _ := New(st, WithClock(func() time.Time { return now }))
	if _, err := m.Put(Definition{ID: "sch-off", Spec: Spec{Kind: Daily, Hour: 9, Minute: 0, Location: "UTC"}, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if claims, _ := m.ClaimDue(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)); len(claims) != 0 {
		t.Fatalf("disabled schedule fired: %+v", claims)
	}
	def, err := m.SetEnabled("sch-off", true)
	if err != nil {
		t.Fatal(err)
	}
	if !def.NextAt.After(now) {
		t.Fatalf("next after enable = %v", def.NextAt)
	}
	if err := m.Delete("sch-off"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := m.Get("sch-off"); ok {
		t.Fatal("deleted schedule still readable")
	}
}

// "N/step" is "N-max/step", not the single value N. The old parser read
// "0/6 * * * *" as midnight only, which silently under-schedules a task a person
// believes runs every six hours.
func TestCronNumericStartStepMeansStartToMax(t *testing.T) {
	utc := time.UTC
	everySix := compile(t, Spec{Kind: Cron, Cron: "0 0/6 * * *", Location: "UTC"})
	after := time.Date(2026, 11, 3, 12, 7, 0, 0, utc)
	want := []time.Time{
		time.Date(2026, 11, 3, 18, 0, 0, 0, utc),
		time.Date(2026, 11, 4, 0, 0, 0, 0, utc),
		time.Date(2026, 11, 4, 6, 0, 0, 0, utc),
		time.Date(2026, 11, 4, 12, 0, 0, 0, utc),
	}
	for i, w := range want {
		got, ok := everySix.Next(after)
		if !ok || !got.Equal(w) {
			t.Fatalf("0/6 occurrence %d = %v ok=%v, want %v", i, got, ok, w)
		}
		after = got
	}

	// 5/10 in minutes: 05,15,25,35,45,55 — not only minute 5.
	everyTen := compile(t, Spec{Kind: Cron, Cron: "5/10 * * * *", Location: "UTC"})
	got, ok := everyTen.Next(time.Date(2026, 11, 3, 12, 0, 0, 0, utc))
	if !ok || !got.Equal(time.Date(2026, 11, 3, 12, 5, 0, 0, utc)) {
		t.Fatalf("5/10 first = %v ok=%v", got, ok)
	}
	got, ok = everyTen.Next(got)
	if !ok || !got.Equal(time.Date(2026, 11, 3, 12, 15, 0, 0, utc)) {
		t.Fatalf("5/10 second = %v ok=%v (the step was read as a single value)", got, ok)
	}

	// "*/15" is unchanged: the explicit step over the whole field.
	quarter := compile(t, Spec{Kind: Cron, Cron: "*/15 * * * *", Location: "UTC"})
	got, ok = quarter.Next(time.Date(2026, 11, 3, 12, 7, 0, 0, utc))
	if !ok || !got.Equal(time.Date(2026, 11, 3, 12, 15, 0, 0, utc)) {
		t.Fatalf("*/15 = %v ok=%v", got, ok)
	}
}

// The DST resolution rules: a nonexistent wall time is skipped, a repeated one
// resolves to the earlier instant, including the 30-minute DST zone and a zone
// whose transition removes midnight.
func TestWallTimeResolvesDSTRules(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// Spring forward: 2026-03-08 02:30 does not exist.
	if _, ok := wallTime(2026, time.March, 8, 2, 30, 0, ny); ok {
		t.Fatal("resolved a wall time in the spring-forward gap")
	}
	// Fall back: 2026-11-01 01:30 occurs twice; the earlier instant is 05:30Z.
	got, ok := wallTime(2026, time.November, 1, 1, 30, 0, ny)
	if !ok {
		t.Fatal("repeated wall time not resolved")
	}
	if want := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("fall-back resolved to %v, want the earlier instant %v", got.UTC(), want)
	}
	// Lord Howe moves by 30 minutes: 2026-10-04 02:15 is inside its gap.
	lh := mustLoc(t, "Australia/Lord_Howe")
	if _, ok := wallTime(2026, time.October, 4, 2, 15, 0, lh); ok {
		t.Fatal("resolved a wall time in Lord Howe's 30-minute gap")
	}
	// São Paulo's DST removed midnight: 2018-11-04 00:00 does not exist.
	sp := mustLoc(t, "America/Sao_Paulo")
	if _, ok := wallTime(2018, time.November, 4, 0, 0, 0, sp); ok {
		t.Fatal("resolved a midnight that São Paulo's DST removed")
	}
}
