// Package schedule owns timed triggers: one-time, fixed interval, daily,
// weekly and five-field cron. It produces due occurrences durably (a Trigger's
// source of truth is the fire row, not a timer) so a restart cannot lose or
// duplicate one. See design §4.7 (Schedule is a workspace-scoped Source) and
// §7.2 (the durable queue the runtime enqueues into).
package schedule

import (
	"fmt"
	"strings"
	"time"
)

// Kind is the shape of a schedule.
type Kind string

const (
	Once   Kind = "at"     // At
	Every  Kind = "every"  // Every
	Daily  Kind = "daily"  // Hour:Minute every day
	Weekly Kind = "weekly" // Hour:Minute on Weekdays
	Cron   Kind = "cron"   // five-field expression
)

// Spec is the persisted definition. It is JSON in schedule.spec; keeping the
// wall-clock fields instead of a precomputed instant is what lets DST rules be
// re-evaluated on every occurrence.
type Spec struct {
	Kind     Kind           `json:"kind"`
	At       time.Time      `json:"at,omitempty"`
	Every    time.Duration  `json:"every,omitempty"`
	Hour     int            `json:"hour,omitempty"`
	Minute   int            `json:"minute,omitempty"`
	Weekdays []time.Weekday `json:"weekdays,omitempty"`
	Cron     string         `json:"cron,omitempty"`
	// Location is an IANA name ("Asia/Shanghai"). Empty means time.Local, which
	// is what a desktop app wants: the operator's clock, not the server's.
	Location string `json:"location,omitempty"`
}

// Schedule is a compiled Spec: the location is loaded once and the cron fields
// are parsed once, so Next costs no parsing in the runtime's poll loop.
type Schedule struct {
	spec Spec
	loc  *time.Location
	cron *cronExpr
}

// Compile validates spec and loads its location.
func Compile(spec Spec) (*Schedule, error) {
	loc := time.Local
	if spec.Location != "" {
		l, err := time.LoadLocation(spec.Location)
		if err != nil {
			return nil, fmt.Errorf("schedule: location %q: %w", spec.Location, err)
		}
		loc = l
	}
	s := &Schedule{spec: spec, loc: loc}
	switch spec.Kind {
	case Once:
		if spec.At.IsZero() {
			return nil, fmt.Errorf("schedule: at needs a time")
		}
	case Every:
		if spec.Every <= 0 {
			return nil, fmt.Errorf("schedule: every needs a positive interval")
		}
	case Daily:
		if err := validClock(spec.Hour, spec.Minute); err != nil {
			return nil, err
		}
	case Weekly:
		if err := validClock(spec.Hour, spec.Minute); err != nil {
			return nil, err
		}
		if len(spec.Weekdays) == 0 {
			return nil, fmt.Errorf("schedule: weekly needs weekdays")
		}
		for _, d := range spec.Weekdays {
			if d < time.Sunday || d > time.Saturday {
				return nil, fmt.Errorf("schedule: weekday %d out of range", d)
			}
		}
	case Cron:
		c, err := parseCron(spec.Cron)
		if err != nil {
			return nil, err
		}
		s.cron = c
	default:
		return nil, fmt.Errorf("schedule: unknown kind %q", spec.Kind)
	}
	return s, nil
}

func validClock(h, m int) error {
	if h < 0 || h > 23 {
		return fmt.Errorf("schedule: hour %d out of range", h)
	}
	if m < 0 || m > 59 {
		return fmt.Errorf("schedule: minute %d out of range", m)
	}
	return nil
}

// Next returns the first occurrence strictly after after, in the schedule's
// local zone. A wall-clock time that does not exist (the spring-forward gap)
// is skipped; a repeated time (fall-back) resolves to the earlier instant.
func (s *Schedule) Next(after time.Time) (time.Time, bool) {
	if after.IsZero() {
		after = time.Now()
	}
	switch s.spec.Kind {
	case Once:
		if s.spec.At.After(after) {
			return s.spec.At, true
		}
		return time.Time{}, false
	case Every:
		return after.Add(s.spec.Every), true
	case Daily:
		return s.nextDaily(after)
	case Weekly:
		return s.nextWeekly(after)
	case Cron:
		return s.cron.next(after, s.loc)
	}
	return time.Time{}, false
}

// Label is how a spec reads to a person in the panel: "每天 09:30", "每 2 小时",
// or the cron expression itself. The core stores the structured spec; only this
// string is for display, so it must never be parsed back (design §6.2).
func (s Spec) Label() string {
	switch s.Kind {
	case Once:
		return "一次 · " + s.At.Format("01-02 15:04")
	case Every:
		return "每 " + shortDuration(s.Every)
	case Daily:
		return fmt.Sprintf("每天 %02d:%02d", s.Hour, s.Minute)
	case Weekly:
		names := make([]string, 0, len(s.Weekdays))
		for _, d := range s.Weekdays {
			names = append(names, weekdayName(d))
		}
		return fmt.Sprintf("每周%s %02d:%02d", strings.Join(names, "、"), s.Hour, s.Minute)
	case Cron:
		return "cron " + s.Cron
	}
	return string(s.Kind)
}

func shortDuration(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d 小时", int(d/time.Hour))
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d 分钟", int(d/time.Minute))
	}
	return d.String()
}

func weekdayName(d time.Weekday) string {
	return [...]string{"日", "一", "二", "三", "四", "五", "六"}[int(d)]
}
