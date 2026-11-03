package domain

// verifier-B: independent golden table for StageOf, written from the 003:9-17
// table (top to bottom, first match wins), not from the owner's tests.

import (
	"testing"
	"time"
)

func TestVerifyStageOfGoldenTable(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	rep := func(s ReportState) *Report { return &Report{State: s, Summary: "x", At: now.Add(-time.Minute)} }
	asg := func(state AssignmentState, r *Report) Assignment {
		a := Assignment{ID: "a", State: state}
		if r != nil {
			a.Report = r
		}
		return a
	}
	closedAt := now.Add(-time.Minute)

	cases := []struct {
		name string
		tk   Ticket
		want Stage
	}{
		{
			// Row 0: ended wins over every other fact.
			"ended beats delivery and attention",
			Ticket{Ended: &Ended{At: now}, Delivery: &DeliveryState{}, Attention: &Attention{},
				Assignments: []Assignment{asg(AssignRunning, nil)}},
			StageEnded,
		},
		{
			// Row 1: a failed delivery, even while a question waits.
			"delivery beats attention",
			Ticket{Delivery: &DeliveryState{Failed: 1}, Attention: &Attention{Question: "q"}},
			StagePersonDelivery,
		},
		{
			"attention before any report",
			Ticket{Attention: &Attention{Question: "q"}, Assignments: []Assignment{asg(AssignRunning, nil)}},
			StagePersonAnswer,
		},
		{
			"report ready",
			Ticket{Assignments: []Assignment{asg(AssignEnded, rep(ReportReady))}},
			StagePersonReview,
		},
		{
			"report blocked and no person message since",
			Ticket{Assignments: []Assignment{asg(AssignEnded, rep(ReportBlocked))}},
			StagePersonBlocked,
		},
		{
			"report failed and no person message since",
			Ticket{Assignments: []Assignment{asg(AssignEnded, rep(ReportFailed))}},
			StagePersonFailed,
		},
		{
			// A person's message after the report lifts the hold; the Assignment is
			// ended, so the first matching row after blocked/failed is idle.
			"blocked released by a later person message",
			Ticket{
				Assignments: []Assignment{asg(AssignEnded, rep(ReportBlocked))},
				Inputs:      []Input{{ID: "i1", Kind: InputTrigger, Human: true, At: now}},
			},
			StageIdle,
		},
		{
			// A retry starts a pending Assignment: WaitsForPerson is false and the
			// queued row wins.
			"blocked with a retried pending assignment is queued",
			Ticket{Assignments: []Assignment{
				asg(AssignEnded, rep(ReportBlocked)), asg(AssignPending, nil),
			}},
			StageWorkingQueued,
		},
		{
			"no assignment at all is queued",
			Ticket{},
			StageWorkingQueued,
		},
		{
			"latest assignment pending is queued",
			Ticket{Assignments: []Assignment{asg(AssignPending, nil)}},
			StageWorkingQueued,
		},
		{
			"report waiting is idle",
			Ticket{Assignments: []Assignment{asg(AssignEnded, rep(ReportWaiting))}},
			StageIdle,
		},
		{
			"assignment ended without a report is idle",
			Ticket{Assignments: []Assignment{asg(AssignEnded, nil)}},
			StageIdle,
		},
		{
			"an assignment still running is working",
			Ticket{Assignments: []Assignment{asg(AssignRunning, rep(ReportProgress))}},
			StageWorking,
		},
		{
			"progress report with no assignment end is working",
			Ticket{Assignments: []Assignment{asg(AssignRunning, nil), asg(AssignRunning, rep(ReportProgress))}},
			StageWorking,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tk := c.tk
			if got := StageOf(&tk, now); got != c.want {
				t.Fatalf("StageOf = %s, want %s", got, c.want)
			}
		})
	}

	// Follows do not create a stage of their own, but an open follow with a ready
	// report is still person/review.
	tk := Ticket{Follows: []Follow{{Thread: "pr", Own: true}}, Assignments: []Assignment{asg(AssignEnded, rep(ReportReady))}}
	if got := StageOf(&tk, now); got != StagePersonReview {
		t.Fatalf("ready + open follow stage = %s, want person/review", got)
	}
	tk.Follows[0].ClosedAt = &closedAt
	if got := StageOf(&tk, now); got != StagePersonReview {
		t.Fatalf("ready + closed follow stage = %s, want person/review", got)
	}
}
