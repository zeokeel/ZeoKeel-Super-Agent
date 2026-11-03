package runtime

// The 30-minute background rule (003:33-39): a job still running after the turn
// ended may keep the Ticket waiting, but not forever. When the limit expires the
// job is killed and the Ticket reports waiting with the job ids and the last
// output it produced; the session is never woken for a killed job (there is no
// result to feed back). Subagents are not shell jobs and are not covered here.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/agent"
	"github.com/zeokeel/super-agent/internal/domain"
)

// defaultBackgroundLimit is the 30-minute rule from 003:39.
const defaultBackgroundLimit = 30 * time.Minute

// backgroundLimit is the configured rule: execution.background_timeout (seconds,
// 30s..24h), default 30m. Tests override it with SetBackgroundLimit.
func (r *Runtime) backgroundLimit() time.Duration {
	r.mu.Lock()
	override := r.bgLimit
	raw := strings.TrimSpace(r.settings["execution.background_timeout"])
	r.mu.Unlock()
	if override > 0 {
		return override
	}
	if n, err := strconv.Atoi(raw); err == nil {
		d := time.Duration(n) * time.Second
		if d >= 30*time.Second && d <= 24*time.Hour {
			return d
		}
	}
	return defaultBackgroundLimit
}

// SetBackgroundLimit overrides the 30m rule (tests, and a deployment that wants a
// different bound). Zero restores the setting/default.
func (r *Runtime) SetBackgroundLimit(d time.Duration) {
	r.mu.Lock()
	r.bgLimit = d
	r.mu.Unlock()
}

// runningJobs are the shell jobs of one Ticket's Runner that are still running.
func (r *Runtime) runningJobs(run *agent.Runner) []agent.Job {
	if run == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out []agent.Job
	for _, job := range run.Jobs().List(ctx) {
		if job.Status == "running" {
			out = append(out, job)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// jobKey identifies one job of one Ticket for the killed-set.
func jobKey(id domain.ID, jobID string) string { return string(id) + "|" + jobID }

func (r *Runtime) markJobKilled(id domain.ID, jobID string) {
	r.jobsMu.Lock()
	if r.bgKilled == nil {
		r.bgKilled = map[string]bool{}
	}
	r.bgKilled[jobKey(id, jobID)] = true
	r.jobsMu.Unlock()
}

func (r *Runtime) jobKilled(id domain.ID, jobID string) bool {
	r.jobsMu.Lock()
	defer r.jobsMu.Unlock()
	return r.bgKilled[jobKey(id, jobID)]
}

// watchJob follows one background job: a normal finish becomes an input that wakes
// the next turn; a finish after the limit becomes a waiting report naming the
// killed job, and never wakes the session.
func (r *Runtime) watchJob(id domain.ID, run *agent.Runner, jobID string) {
	waitDone := make(chan agent.Job, 1)
	go func() {
		job, err := run.Jobs().Wait(context.Background(), jobID)
		if err != nil {
			job = agent.Job{ID: jobID, Status: "error", Error: err.Error()}
		}
		waitDone <- job
	}()

	limit := r.backgroundLimit()
	select {
	case job := <-waitDone:
		if r.jobKilled(id, jobID) {
			return // the timeout path already reported it; never wake for killed
		}
		if t, ok := r.ticketSnapshot(id); ok && t.Ended != nil {
			return
		}
		if r.deliverJobResult(id, job) {
			r.Publish()
		}
	case <-time.After(limit):
		if t, ok := r.ticketSnapshot(id); ok && t.Ended != nil {
			return
		}
		r.markJobKilled(id, jobID)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = run.Jobs().Cancel(ctx, jobID, "background timeout")
		job, _ := run.Jobs().Get(ctx, jobID)
		text := fmt.Sprintf("后台作业超过 %s 仍未结束，已终止：%s", humanDuration(limit), jobID)
		if strings.TrimSpace(job.Output) != "" {
			text += "\n最后输出：\n" + clipRunes(job.Output, 1000)
		}
		if job.Error != "" {
			text += "\n错误：" + job.Error
		}
		_ = r.reportTicket(id, domain.ReportWaiting, text)
		r.log.append(string(id), workerEvent{kind: "notice", notice: true, text: text})
		r.slogger().Warn("background job killed after the limit",
			"ticket", id, "job", jobID, "limit", limit.String())
		r.Publish()
	}
}

// deliverJobResult records a finished job as an Input and wakes the Ticket, so the
// next turn reads its output. Killed jobs never get here (watchJob checks first).
func (r *Runtime) deliverJobResult(id domain.ID, job agent.Job) bool {
	text := fmt.Sprintf("后台作业 %s 结束（状态 %s，退出码 %d）。", job.ID, job.Status, job.ExitCode)
	if strings.TrimSpace(job.Output) != "" {
		text += "\n输出摘要：\n" + clipRunes(job.Output, 2000)
	}
	if job.Error != "" {
		text += "\n错误：" + job.Error
	}
	text += "\n用 job_output（task_id=" + job.ID + "）读取完整结果，然后继续任务。"
	in := domain.Input{
		ID:   domain.Key(domain.WorkspaceScope(""), "input", id, "job", job.ID),
		Kind: domain.InputNote, Text: text, At: r.clock(),
	}
	if err := r.deliverInput(id, in); err != nil {
		r.slogger().Warn("job result delivery failed", "ticket", id, "job", job.ID, "error", err)
		return false
	}
	return true
}
