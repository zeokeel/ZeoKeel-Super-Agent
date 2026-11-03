package runtime

// Reply composition is per channel and per authorization. A result that is fine
// for the operator's own panel is not automatically fine to send to a colleague:
// the Decision's ReplyPlan says who may be answered (design §4.10, 003:41-44).
//
//   - IM (feishu/dingtalk): short Chinese, conclusion first, <= imMaxRunes;
//   - GitHub: structured (background / changes / verification / artifacts);
//   - local panel: concise.
//
// A notify-mode (or "needs the operator") plan never sends to the colleague's
// channel directly: the text is recorded for the operator, and delivery waits for
// a person. That is the difference between "the agent has a draft" and "the agent
// spoke for me".

import (
	"fmt"
	"strings"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/outbox"
	"github.com/zeokeel/super-agent/internal/view"
)

// imMaxRunes bounds one IM reply. Chinese chat replies are read on a phone; a
// wall of text is not an answer. The check is on runes, not bytes.
const imMaxRunes = 100

// Reply is one outbound message the runtime produced for a Ticket. It is kept
// even when no external channel is wired, so the panel can show what would have
// been said (design §4.10: an answer is a fact, whether or not it was delivered).
type Reply struct {
	Ticket string
	Source string
	Text   string
	Reason string
	// Held marks a reply that must not go to the requester's channel yet: the
	// ReplyPlan routes it to the operator (notify / both / none).
	Held bool
}

// Replies returns the replies produced so far, newest last.
func (r *Runtime) Replies() []Reply {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Reply(nil), r.replies...)
}

// composeReply turns a Ticket's conclusion into the message for one channel.
func composeReply(source string, t domain.Ticket, report *domain.Report, artifacts []domain.Artifact) (text, reason string) {
	switch source {
	case "feishu", "dingtalk":
		return composeIMReply(report, artifacts)
	case "github":
		return composeGitHubReply(t, report, artifacts)
	default:
		return composeLocalReply(report, artifacts)
	}
}

// composeLocalReply is the panel/operator wording: a short factual conclusion.
func composeLocalReply(report *domain.Report, artifacts []domain.Artifact) (string, string) {
	head, reason := conclusion(report)
	var b strings.Builder
	b.WriteString(head)
	if len(artifacts) > 0 {
		b.WriteString("\n交付物：")
		for _, a := range artifacts {
			b.WriteString("\n- ")
			b.WriteString(a.Title)
			if a.Detail != "" {
				b.WriteString("（" + a.Detail + "）")
			}
		}
	}
	return b.String(), reason
}

// composeGitHubReply is the structured form a PR review expects: what was asked,
// what changed, how it was verified, what came out. GitHub has no length limit
// that matters here, so structure beats compression.
func composeGitHubReply(t domain.Ticket, report *domain.Report, artifacts []domain.Artifact) (string, string) {
	head, reason := conclusion(report)
	var b strings.Builder
	b.WriteString("**背景**\n")
	b.WriteString(firstLine(t.Title))
	b.WriteString("\n\n**改动**\n")
	if len(artifacts) == 0 {
		b.WriteString("- 见工单记录\n")
	} else {
		for _, a := range artifacts {
			b.WriteString("- " + a.Title)
			if a.Detail != "" {
				b.WriteString("（" + a.Detail + "）")
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n**验证**\n")
	b.WriteString(head)
	b.WriteString("\n\n**交付物**\n")
	if len(artifacts) == 0 {
		b.WriteString("- 无\n")
	} else {
		for _, a := range artifacts {
			b.WriteString("- " + a.Title + "\n")
		}
	}
	return strings.TrimSpace(b.String()), reason
}

// composeIMReply is the phone form: one short conclusion (and the one thing still
// needed), never more than imMaxRunes runes. Truncation happens on a rune
// boundary with an ellipsis so a Chinese character is never split.
func composeIMReply(report *domain.Report, artifacts []domain.Artifact) (string, string) {
	head, reason := conclusion(report)
	text := head
	if len(artifacts) > 0 {
		text += "；产出：" + artifactTitles(artifacts, 2)
	}
	return clipRunes(text, imMaxRunes), reason
}

// artifactTitles joins a few deliverable titles for a one-line IM answer.
func artifactTitles(artifacts []domain.Artifact, max int) string {
	titles := make([]string, 0, max)
	for _, a := range artifacts {
		titles = append(titles, a.Title)
		if len(titles) == max {
			break
		}
	}
	out := strings.Join(titles, "、")
	if len(artifacts) > len(titles) {
		out += fmt.Sprintf(" 等 %d 项", len(artifacts))
	}
	return out
}

// conclusion is the one-sentence result of a Report, shared by all channels.
func conclusion(report *domain.Report) (head, reason string) {
	switch {
	case report == nil:
		return "处理完成。", "result"
	case report.State == domain.ReportFailed:
		return "没能完成：" + report.Summary, "failed"
	case report.State == domain.ReportBlocked:
		return "卡住了，需要你处理：" + report.Summary, "blocked"
	case report.State == domain.ReportDropped:
		if report.Summary == "" {
			return "这条不需要处理", "dropped"
		}
		return "这条不需要处理：" + report.Summary, "dropped"
	default:
		if strings.TrimSpace(report.Summary) == "" {
			return "已完成。", "result"
		}
		return "已完成：" + report.Summary, "result"
	}
}

// firstSentence shortens a summary to one sentence-ish clause.
func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for i, r := range s {
		if r == '。' || r == '\n' || r == '！' || r == '？' {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

// clipRunes caps s at n runes, adding an ellipsis when it was cut.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// replyPlanFor finds the ReplyPlan that authorized answering this Ticket: the
// Decision that created it carries the route, the purpose and the basis.
func (r *Runtime) replyPlanFor(t domain.Ticket) (domain.ReplyPlan, bool) {
	for _, id := range t.DecisionIDs {
		if d, ok := r.eng.Decision(id); ok {
			return d.Reply, true
		}
	}
	return domain.ReplyPlan{}, false
}

// mayReplyDirectly answers "is the requester's own channel allowed?". The
// operator's own message (self/local) is always answerable; anyone else needs a
// direct ReplyPlan. notify-mode text is a draft for the operator, not a message.
func mayReplyDirectly(trigger domain.Trigger, plan domain.ReplyPlan, havePlan bool) bool {
	if trigger.Self || trigger.Source == "local" {
		return true
	}
	if !havePlan {
		// No plan means no authorization was recorded: fail closed.
		return false
	}
	return plan.Route == domain.ReplyDirect
}

// deliverReply sends a Ticket's conclusion back to the conversation the request
// came from, if the ReplyPlan allows speaking to that person directly. Otherwise
// the text is recorded as held for the operator and nothing leaves the machine.
func (r *Runtime) deliverReply(t domain.Ticket) {
	trigger, ok := r.eng.Trigger(t.Origin)
	if !ok || trigger.Channel.Source == "" {
		return // no conversation to answer; the panel is the only audience
	}
	report := t.LastReport()
	artifacts := r.eng.Artifacts(t.ID)
	text, reason := composeReply(trigger.Source, t, report, artifacts)
	plan, havePlan := r.replyPlanFor(t)
	direct := mayReplyDirectly(trigger, plan, havePlan)

	r.mu.Lock()
	r.replies = append(r.replies, Reply{
		Ticket: fmt.Sprintf("#%d", t.Number), Source: trigger.Source,
		Text: text, Reason: reason, Held: !direct,
	})
	// The key is derived from the Ticket and the reason, so a retry after a crash
	// is the same message rather than a second one.
	key := string(domain.Key(domain.WorkspaceScope(t.Workspace), "reply", t.ID, reason))
	ob := r.ob
	r.mu.Unlock()

	if !direct {
		// The plan routes this to the operator. There is no operator channel in
		// this build, so the panel holds the draft; sending it to the colleague
		// would be the agent speaking for the operator (design §4.10).
		r.log.append(string(t.ID), workerEvent{kind: "reply", notice: true,
			text: "拟回复（未直发同事，等待你处理）：\n" + text})
		r.slogger().Info("reply held for the operator",
			"ticket", fmt.Sprintf("#%d", t.Number), "source", trigger.Source)
		r.Publish()
		return
	}

	r.log.append(string(t.ID), workerEvent{kind: "reply", text: text, notice: true})
	if ob != nil {
		conv := trigger.Conversation.ID
		thread := trigger.Channel.Thread
		if thread == "" && len(trigger.Threads) > 0 {
			thread = trigger.Threads[0].ID
		}
		if _, err := ob.Send(outbox.Message{
			ID: key, Workspace: t.Workspace, Ticket: t.ID,
			Source: trigger.Source, Conversation: conv, Thread: thread,
			ReplyTo: string(trigger.ID), Kind: outbox.KindReply, Text: text,
		}); err != nil {
			r.slogger().Warn("reply could not be queued for delivery",
				"ticket", fmt.Sprintf("#%d", t.Number), "source", trigger.Source, "error", err)
		}
	}
	r.Publish()
}

// replyLines projects the runtime's replies into the message stream.
func (r *Runtime) replyLines(t domain.Ticket) []view.Line {
	var out []view.Line
	for _, rep := range r.Replies() {
		if rep.Ticket != fmt.Sprintf("#%d", t.Number) {
			continue
		}
		out = append(out, view.Line{Kind: view.LineReply, At: r.clock(),
			Text: rep.Text, Badges: []string{rep.Source}})
	}
	return out
}
