package runtime

// Startup crash recovery for Assignments. The process that owned a "running"
// Assignment is gone by the time this runs; the Ticket still exists with its
// inputs, so the honest recovery is: mark the interruption as a note Input, open a
// fresh Assignment and wake the Worker with a recovery instruction (005 crash
// recovery, 003:112, wave2-spec §3 Assignment recovery).
//
// Work is taken oldest first, so a long queue resumes in arrival order.

import (
	"sort"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// RecoverAssignments finds stale running Assignments and re-launches them. It runs
// once at startup, before the 2s pass accepts new work. Returns how many were
// recovered; a single Ticket failing does not stop the others.
func (r *Runtime) RecoverAssignments(now time.Time) int {
	tickets := r.ticketSnapshots()
	sort.Slice(tickets, func(i, j int) bool { return tickets[i].CreatedAt.Before(tickets[j].CreatedAt) })
	recovered := 0
	for _, t := range tickets {
		if t.Ended != nil {
			continue
		}
		as := t.LastAssignment()
		if as.State != domain.AssignRunning {
			continue
		}
		// The engine owns the interruption fact (003:146): it ends the running
		// Assignment as hostRestarted, opens a fresh pending one with the same
		// SessionID, and queues the recovery note Input. Persistence commits all
		// three in one store transaction.
		r.mu.Lock()
		p := r.persist
		r.mu.Unlock()
		if p != nil {
			if err := p.InterruptAssignment(t.ID); err != nil {
				r.slogger().Error("crash recovery: interrupt assignment failed",
					"ticket", t.Number, "error", err)
				continue
			}
		} else if err := r.eng.InterruptAssignment(t.ID); err != nil {
			r.slogger().Error("crash recovery: interrupt assignment failed",
				"ticket", t.Number, "error", err)
			continue
		}
		fresh, ok := r.ticketSnapshot(t.ID)
		if !ok {
			continue
		}
		if err := r.wakeTicket(fresh); err != nil {
			r.slogger().Error("crash recovery: wake failed", "ticket", t.Number, "error", err)
			continue
		}
		r.log.append(string(t.ID), workerEvent{kind: "notice", notice: true,
			text: "主机重启：上一次运行被中断，已重新排队"})
		recovered++
	}
	if recovered > 0 {
		r.slogger().Info("recovered interrupted assignments", "count", recovered)
		r.Publish()
	}
	return recovered
}
