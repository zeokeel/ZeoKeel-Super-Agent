package main

import (
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/schedule"
)

// A timer becomes the same Trigger shape a chat message produces, so nothing
// downstream needs to special-case timers.
func TestScheduleFireBecomesATrigger(t *testing.T) {
	c := schedule.Claim{
		FireID:    "fire-1",
		PlannedAt: time.Unix(1700000000, 0),
		FiredAt:   time.Unix(1700000002, 0),
		Status:    "fired",
		Definition: schedule.Definition{
			ID: "daily-report", WorkspaceID: "ws_acme_data",
		},
	}
	tr := triggerForSchedule(c)
	if tr.ID != "fire-1" || tr.Source != "schedule" {
		t.Fatalf("bad trigger: %+v", tr)
	}
	if tr.DefaultWorkspace != "ws_acme_data" {
		t.Fatal("the timer's workspace must travel with the trigger")
	}
	if !tr.Addressed.Direct || !tr.Human {
		t.Fatal("a timer is an instruction from the operator, not ambient chatter")
	}
	if tr.At.IsZero() || tr.Text == "" {
		t.Fatal("the trigger must carry a time and a task text")
	}
}
