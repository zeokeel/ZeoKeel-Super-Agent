package runtime

// PR/Issue lifecycle: a GitHub trigger that says a pull request merged or closed
// ends the Tickets following that Thread (003:96-106). The Source only reports
// facts; ending the Ticket is the domain's call, made here through ThreadClosed.
//
// A Worker that opens a PR also needs the Ticket bound to that Thread before the
// end matters; the `follow` tool (runtime/artifacts.go) does that with
// BindFollow(own=true). Without it the Ticket ends by its own ready report; with
// it, a ready report waits until the PR closes and ends with the PR's outcome.

import (
	"errors"
	"strings"

	"github.com/zeokeel/super-agent/internal/domain"
)

// closingThreadOutcome inspects a GitHub Trigger's Channel.Address and reports
// whether it says a thread finished, and with which outcome. Only pull requests
// and issues use it; comments/CI carry no state.
func closingThreadOutcome(t domain.Trigger) (outcome string, closed bool) {
	if t.Source != "github" || t.Channel.Address == nil {
		return "", false
	}
	state := strings.ToLower(t.Channel.Address["state"])
	switch {
	case strings.Contains(state, "merged"):
		return domain.OutcomeMerged, true
	case strings.Contains(state, "closed"):
		return domain.OutcomeClosed, true
	}
	return "", false
}

// CloseThread records a Thread close and lets the engine end the Tickets that are
// finished by it. It returns the outcome it applied ("" when the trigger was not
// a close). Repeated closes are harmless: ThreadClosed is idempotent per Ticket.
func (r *Runtime) CloseThread(t domain.Trigger) (string, error) {
	outcome, closed := closingThreadOutcome(t)
	if !closed {
		return "", nil
	}
	at := t.At
	if at.IsZero() {
		at = r.clock()
	}
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	// Collect the exact keys to close: the trigger's own Thread key, plus any
	// Ticket Follow whose key names the same Thread but was bound under a
	// different scope (a panel ticket following a PR from a workspace Source).
	keys := map[string]bool{}
	for _, ref := range t.Threads {
		keys[domain.ThreadKey(t.Scope, ref.Source, ref.ID)] = true
		suffix := "|" + ref.Source + "|" + ref.ID
		for _, tk := range r.ticketSnapshots() {
			for _, f := range tk.Follows {
				if f.Thread == ref.ID || strings.HasSuffix(f.Thread, suffix) {
					keys[f.Thread] = true
				}
			}
		}
	}
	lastErr := error(nil)
	for key := range keys {
		var err error
		if p != nil {
			err = p.ThreadClosed(key, outcome, at)
		} else {
			err = r.eng.ThreadClosed(key, outcome, at)
		}
		if err != nil {
			lastErr = err
			r.slogger().Warn("thread close failed", "thread", key, "outcome", outcome, "error", err)
		}
	}
	if lastErr != nil {
		return outcome, lastErr
	}
	r.Publish()
	return outcome, nil
}

// BindThread makes a Ticket follow a Thread it opened (a pull request). The
// Ticket then ends when that Thread closes, with merged/closed as its outcome.
func (r *Runtime) BindThread(ticketID domain.ID, thread string, own bool) error {
	thread = strings.TrimSpace(thread)
	if thread == "" {
		return errNoThread
	}
	if !strings.Contains(thread, "|") {
		// The Worker names a GitHub thread as "owner/repo#12"; the engine keys
		// Follows by "<scope>|<source>|<id>". Derive the scope/source from the
		// Ticket's origin, defaulting to the global GitHub source.
		scope, source := domain.Global, "github"
		if tk, ok := r.ticketSnapshot(ticketID); ok {
			if tr, ok := r.eng.Trigger(tk.Origin); ok {
				scope = tr.Scope
			}
		}
		thread = domain.ThreadKey(scope, source, thread)
	}
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	var err error
	if p != nil {
		err = p.BindFollow(ticketID, thread, own)
	} else {
		err = r.eng.BindFollow(ticketID, thread, own)
	}
	if err != nil {
		return err
	}
	r.log.append(string(ticketID), workerEvent{kind: "notice", notice: true,
		text: "已关注 Thread " + thread + "（own=" + boolText(own) + "）；关闭时按 PR 结果结束工单"})
	r.Publish()
	return nil
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

var errNoThread = errors.New("runtime: follow needs a thread id like owner/repo#12")
