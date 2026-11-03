package ui

import (
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// The runtime's action channels, as the window sees them. The window never
// imports the runtime (design §6.2): it asks whether the Commander it was given
// also implements one of these, and when it does not, the action is shown
// disabled with the reason — a button that silently does nothing is worse than
// a disabled one that says why (005 "诚实状态").
//
// The runtime is adding these methods; cmd wires the same object as Commander,
// so implementing them is enough for the panel to use them.
type AttentionAnswerer interface {
	AnswerAttention(ticketID, answer string) error
}

type TicketStopper interface {
	StopTicket(ticketID, reason string) error
}

type TicketRetrier interface {
	RetryTicket(ticketID string) error
}

type ExecutionPauser interface {
	PauseExecution(paused bool) error
}

// stopReason is what the panel records when the operator stops a Ticket from
// 总览. It names the place, so the audit trail says where the stop came from.
const stopReason = "操作员在总览停止"

// actionChannels says which action channels the command object implements.
type actionChannels struct {
	answer bool
	retry  bool
	stop   bool
	pause  bool
}

func (a *App) actionChannels() actionChannels {
	var ch actionChannels
	if a.cmd == nil {
		return ch
	}
	_, ch.answer = a.cmd.(AttentionAnswerer)
	_, ch.retry = a.cmd.(TicketRetrier)
	_, ch.stop = a.cmd.(TicketStopper)
	_, ch.pause = a.cmd.(ExecutionPauser)
	return ch
}

// wired reports whether the action can be sent.
func (ch actionChannels) wired(act view.TicketAction) bool {
	switch act {
	case view.ActionAnswer:
		return ch.answer
	case view.ActionRetry:
		return ch.retry
	case view.ActionStop:
		return ch.stop
	}
	return false
}

// interfaceName is the optional interface an action needs, so a missing channel
// names the exact method set the runtime has to provide.
func interfaceName(act view.TicketAction) string {
	switch act {
	case view.ActionAnswer:
		return "AttentionAnswerer"
	case view.ActionRetry:
		return "TicketRetrier"
	case view.ActionStop:
		return "TicketStopper"
	}
	return ""
}

// missingActions is the actions the rows offer but the command has no channel
// for, in the order they were met, each named with its method set.
func (a *App) missingActions(rows []view.TicketRow) []string {
	ch := a.actionChannels()
	seen := map[view.TicketAction]bool{}
	var out []string
	for _, r := range rows {
		for _, act := range view.ActionsFor(r) {
			if ch.wired(act) || seen[act] {
				continue
			}
			seen[act] = true
			out = append(out, act.Label()+"（"+interfaceName(act)+"）")
		}
	}
	return out
}

// missingActionsNote is the honest line under 需要我处理 when some actions cannot
// be sent: it says which, and which interface the runtime is missing.
func (a *App) missingActionsNote(rows []view.TicketRow) string {
	missing := a.missingActions(rows)
	if len(missing) == 0 {
		return ""
	}
	return "动作未接线：运行时未提供 " + strings.Join(missing, "、")
}

// overviewActions draws one row's actions. They are the only place a work-item
// action exists (design §6.3): 回答, 重试 and 停止.
func (a *App) overviewActions(c *mui.Context, r view.TicketRow) {
	ch := a.actionChannels()
	for _, act := range view.ActionsFor(r) {
		label := act.Label()
		btn := mui.Button(c, label).Label(label).Height(24).FontSize(fs(13))
		if !ch.wired(act) {
			// Disabled, and the band's note says why; no click is handled below,
			// so a disabled button cannot fall through to a no-op that looks sent.
			btn.Disabled(true)
			continue
		}
		if !btn.Clicked() {
			continue
		}
		switch act {
		case view.ActionAnswer:
			a.openAnswer(r)
		case view.ActionRetry:
			a.retryTicket(r)
		case view.ActionStop:
			a.openStop(r)
		}
	}
}

// openAnswer opens the 回答 dialog for a row. The question is the Attention's
// own when the projector published it; otherwise the row's detail line stands in
// as the question and only a free-text answer is offered, which is honest: the
// panel does not invent options.
func (a *App) openAnswer(r view.TicketRow) {
	a.answerRow = &r
	a.answerDraft = ""
	a.answerErr = ""
}

// answerDialog is the 回答 modal: the question, its structured options, and a
// free-text answer.
func (a *App) answerDialog(c *mui.Context) {
	if a.answerRow == nil {
		return
	}
	tk := a.tk
	r := *a.answerRow
	open := true
	mui.Modal(c, &open, func() {
		mui.Column(c).Width(560).Gap(10).Children(func() {
			mui.Text(c, "回答 #"+r.ID).FontSize(fs(17)).Bold().TextColor(tk.Text)
			question := r.Detail
			if r.Attention != nil && r.Attention.Question != "" {
				question = r.Attention.Question
			}
			mui.Text(c, question).FontSize(fs(15)).TextColor(tk.Text)
			if r.Attention != nil && len(r.Attention.Options) > 0 {
				mui.Text(c, "选一个，或直接写你的回答").FontSize(fs(12)).TextColor(tk.TextFaint)
				mui.Row(c).Gap(6).Wrap().Children(func() {
					for _, opt := range r.Attention.Options {
						opt := opt
						if mui.Button(c, opt).Label(opt).Clicked() {
							a.submitAnswer(opt)
						}
					}
				})
			}
			field := mui.TextInput(c.Key("answer-text"), &a.answerDraft).
				Placeholder("回答…").Label("回答").AutoFocus()
			if field.Submitted() {
				a.submitAnswer(a.answerDraft)
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				if a.answerErr != "" {
					mui.Text(c, a.answerErr).FontSize(fs(12)).TextColor(tk.Red)
				}
				mui.Spacer(c)
				if mui.Button(c, "取消").Clicked() {
					a.closeAnswer()
				}
				if strings.TrimSpace(a.answerDraft) != "" {
					if mui.PrimaryButton(c, "提交回答").Label("提交回答").Clicked() {
						a.submitAnswer(a.answerDraft)
					}
				}
			})
		})
	})
	if !open {
		a.closeAnswer()
	}
}

func (a *App) closeAnswer() {
	a.answerRow = nil
	a.answerDraft = ""
	a.answerErr = ""
}

// submitAnswer sends one answer through the answer channel. It never pretends:
// without the channel it sets the reason, and a failed send keeps the dialog open
// with the runtime's error instead of closing as if it worked.
func (a *App) submitAnswer(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if a.answerRow == nil {
		return
	}
	answerer, ok := a.cmd.(AttentionAnswerer)
	if !ok || answerer == nil {
		a.answerErr = "回答通道未接线：运行时未实现 AttentionAnswerer"
		return
	}
	if err := answerer.AnswerAttention(a.answerRow.ID, text); err != nil {
		a.answerErr = err.Error()
		return
	}
	a.actionErr = ""
	a.closeAnswer()
}

// retryTicket asks the runtime to run the Ticket again.
func (a *App) retryTicket(r view.TicketRow) {
	retrier, ok := a.cmd.(TicketRetrier)
	if !ok || retrier == nil {
		a.actionErr = "重试通道未接线：运行时未实现 TicketRetrier"
		return
	}
	if err := retrier.RetryTicket(r.ID); err != nil {
		a.actionErr = err.Error()
		return
	}
	a.actionErr = ""
}

// openStop opens the confirmation for 停止. It is the one action that ends a
// Ticket for good, so it asks first.
func (a *App) openStop(r view.TicketRow) {
	a.stopRow = &r
	a.actionErr = ""
}

// stopDialog confirms 停止 and sends it with the panel's reason.
func (a *App) stopDialog(c *mui.Context) {
	if a.stopRow == nil {
		return
	}
	tk := a.tk
	r := *a.stopRow
	open := true
	mui.Modal(c, &open, func() {
		mui.Column(c).Width(520).Gap(10).Children(func() {
			mui.Text(c, "停止 #"+r.ID+"？").FontSize(fs(17)).Bold().TextColor(tk.Text)
			mui.Text(c, r.Title).FontSize(fs(14)).TextColor(tk.TextMuted)
			mui.Text(c, "停止会结束这张工单并撤销它的 Mandate；执行器会停下，之后只能由新的请求重新开始。").
				FontSize(11).TextColor(tk.TextMuted)
			if a.actionErr != "" {
				mui.Text(c, a.actionErr).FontSize(fs(12)).TextColor(tk.Red)
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Spacer(c)
				if mui.Button(c, "取消").Clicked() {
					a.stopRow = nil
					a.actionErr = ""
				}
				if mui.PrimaryButton(c, "确认停止").Label("确认停止").Clicked() {
					a.confirmStop(r)
				}
			})
		})
	})
	if !open {
		a.stopRow = nil
	}
}

func (a *App) confirmStop(r view.TicketRow) {
	stopper, ok := a.cmd.(TicketStopper)
	if !ok || stopper == nil {
		a.actionErr = "停止通道未接线：运行时未实现 TicketStopper"
		return
	}
	if err := stopper.StopTicket(r.ID, stopReason); err != nil {
		a.actionErr = err.Error()
		return
	}
	a.actionErr = ""
	a.stopRow = nil
}
