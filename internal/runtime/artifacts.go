package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/zeokeel/super-agent/internal/agent"
	"github.com/zeokeel/super-agent/internal/domain"
)

// workerTools are the domain-facing tools a Worker gets on top of the file and
// shell ones: it reports status and presents deliverables. They are the only way
// a Worker can say anything to the domain, which keeps the domain in charge
// (design §5.3).
func (r *Runtime) workerTools(t domain.Ticket) []tool.BaseTool {
	report, err := utils.InferTool("report",
		"Report this ticket's state to the person who asked. Call it when the work is done (ready), "+
			"at meaningful milestones (progress), or when you cannot continue (blocked/failed). "+
			"A summary is required: it is what the reply to the requester says.",
		func(_ context.Context, in struct {
			State   string `json:"state" jsonschema:"description=progress|waiting|ready|blocked|failed|dropped"`
			Summary string `json:"summary" jsonschema:"description=one or two sentences for the person reading the ticket"`
		}) (string, error) {
			state := domain.ReportState(in.State)
			switch state {
			case domain.ReportProgress, domain.ReportWaiting, domain.ReportReady,
				domain.ReportBlocked, domain.ReportFailed, domain.ReportDropped:
			default:
				return "", fmt.Errorf("unknown report state %q", in.State)
			}
			if err := r.reportTicket(t.ID, state, in.Summary); err != nil {
				return "", err
			}
			return "reported " + in.State, nil
		})
	if err != nil {
		return nil
	}

	present, err := utils.InferTool("present",
		"Present a deliverable this ticket produced (file, report, PR, link). "+
			"Call it once per deliverable before reporting ready, so it appears on the panel and in the reply.",
		func(_ context.Context, in struct {
			Kind   string `json:"kind" jsonschema:"description=file|image|report|pr|link"`
			Title  string `json:"title" jsonschema:"description=what this deliverable is"`
			Detail string `json:"detail,omitempty" jsonschema:"description=one line the list shows, such as size or changed lines"`
			Path   string `json:"path,omitempty" jsonschema:"description=path relative to the workspace"`
			URL    string `json:"url,omitempty" jsonschema:"description=remote address, such as a pull request"`
		}) (string, error) {
			kind := domain.ArtifactKind(in.Kind)
			switch kind {
			case domain.ArtifactFile, domain.ArtifactImage, domain.ArtifactReport,
				domain.ArtifactPR, domain.ArtifactLink:
			default:
				kind = domain.ArtifactFile
			}
			if _, err := r.eng.Present(t.ID, domain.Artifact{
				Kind: kind, Title: in.Title, Detail: in.Detail, Path: in.Path, URL: in.URL,
			}); err != nil {
				return "", err
			}
			r.mu.Lock()
			p := r.persist
			r.mu.Unlock()
			if p != nil {
				_ = p.SaveTicket(t.ID)
			}
			r.Publish()
			return "presented " + in.Title, nil
		})
	if err != nil {
		return nil
	}
	ask, err := utils.InferTool("ask_user_question",
		"Ask the operator a question when a decision belongs to them. The ticket then waits for an answer.",
		func(_ context.Context, in struct {
			Question string   `json:"question" jsonschema:"description=one specific question, with the options if there are any"`
			Options  []string `json:"options,omitempty" jsonschema:"description=the choices worth offering, when they are enumerable"`
		}) (string, error) {
			q := strings.TrimSpace(in.Question)
			if q == "" {
				return "", fmt.Errorf("a question needs text")
			}
			if err := r.askQuestion(t.ID, q, in.Options); err != nil {
				return "", err
			}
			return "已把问题放到待办：等待操作员回答", nil
		})
	if err != nil {
		return nil
	}

	follow, err := utils.InferTool("follow",
		"Bind this Ticket to a pull request or issue Thread it opened, so the Ticket ends "+
			"when that Thread closes with the PR outcome (merged/closed) instead of finishing early.",
		func(_ context.Context, in struct {
			Thread string `json:"thread" jsonschema:"description=thread id such as owner/repo#12"`
			Mode   string `json:"mode,omitempty" jsonschema:"description=own (opened by you, default) | context (only follow for context)"`
		}) (string, error) {
			own := in.Mode != "context"
			if err := r.BindThread(t.ID, in.Thread, own); err != nil {
				return "", err
			}
			return "已绑定 Thread " + in.Thread, nil
		})
	if err != nil {
		return nil
	}

	out := []tool.BaseTool{report, present, ask, follow}

	// DSH executor parity: ack / handback / thread_unrelated / request_status.
	ack, err := utils.InferTool("ack",
		"Send a short acknowledgement to the person who asked (the message came from a chat channel). "+
			"Use it when the work will take a while, so the requester knows it was heard.",
		func(_ context.Context, in struct {
			Text string `json:"text" jsonschema:"description=one short sentence to send back"`
		}) (string, error) {
			text := strings.TrimSpace(in.Text)
			if text == "" {
				return "", fmt.Errorf("ack text is empty")
			}
			r.mu.Lock()
			p := r.persist
			r.mu.Unlock()
			if p != nil {
				if _, err := p.Reply(t.ID, text); err != nil {
					return "", err
				}
				return "已发送确认", nil
			}
			r.eng.Reply(t.ID, text)
			return "已发送确认", nil
		})
	if err != nil {
		return nil
	}
	handback, err := utils.InferTool("handback",
		"Hand a thread back to the person: say you cannot own it and why. "+
			"The message returns to the operator instead of being answered by you.",
		func(_ context.Context, in struct {
			Thread string `json:"thread" jsonschema:"description=thread id, e.g. dm_1 or owner/repo#12"`
			Reason string `json:"reason" jsonschema:"description=why you are handing it back"`
		}) (string, error) {
			thread := strings.TrimSpace(in.Thread)
			if thread == "" {
				return "", fmt.Errorf("thread is required")
			}
			r.mu.Lock()
			p := r.persist
			r.mu.Unlock()
			var err error
			if p != nil {
				err = p.HandBack(t.ID, thread, in.Reason)
			} else {
				err = r.eng.HandBack(t.ID, thread, in.Reason)
			}
			if err != nil {
				return "", err
			}
			r.Publish()
			return "已交回给操作员", nil
		})
	if err != nil {
		return nil
	}
	unrelated, err := utils.InferTool("thread_unrelated",
		"Mark a thread as unrelated to this ticket, so the person's messages there are handed "+
			"back instead of silently answered. Cannot be the ticket's own thread.",
		func(_ context.Context, in struct {
			Thread string `json:"thread" jsonschema:"description=thread id"`
			Reason string `json:"reason" jsonschema:"description=why it is unrelated"`
		}) (string, error) {
			thread := strings.TrimSpace(in.Thread)
			if thread == "" {
				return "", fmt.Errorf("thread is required")
			}
			r.mu.Lock()
			p := r.persist
			r.mu.Unlock()
			var err error
			if p != nil {
				err = p.MarkUnrelated(t.ID, thread, in.Reason)
			} else {
				err = r.eng.MarkUnrelated(t.ID, thread, in.Reason)
			}
			if err != nil {
				return "", err
			}
			r.Publish()
			return "已标记为无关，消息会交回操作员", nil
		})
	if err != nil {
		return nil
	}
	status, err := utils.InferTool("request_status",
		"Read this ticket's current status (stage, last report, running/queued) without changing it.",
		func(_ context.Context, _ struct{}) (string, error) {
			snap, ok := r.ticketSnapshot(t.ID)
			if !ok {
				return "", fmt.Errorf("ticket not found")
			}
			st := domain.StageOf(&snap, r.clock())
			report := ""
			if rep := snap.LastReport(); rep != nil {
				report = string(rep.State) + "：" + rep.Summary
			}
			return fmt.Sprintf("ticket #%d stage=%s report=%s", snap.Number, st, report), nil
		})
	if err != nil {
		return nil
	}
	out = append(out, ack, handback, unrelated, status)
	out = append(out, r.capabilityTools()...)
	out = append(out, r.skillTools(t)...)
	return out
}

// skillTools exposes the skill library the prompt advertises. Without load_skill the
// prompt's "open the full skill by name" line is a promise nothing can keep.
func (r *Runtime) skillTools(t domain.Ticket) []tool.BaseTool {
	r.mu.Lock()
	set := r.skills
	r.mu.Unlock()
	if set == nil {
		return nil
	}
	load, err := utils.InferTool("load_skill",
		"Load the full text of one skill from the library, by name. "+
			"Use it when the short description in the prompt is not enough.",
		func(_ context.Context, in struct {
			Name string `json:"name" jsonschema:"description=the skill name shown in the prompt"`
		}) (string, error) {
			name := strings.TrimSpace(in.Name)
			if !r.skillAllowed(t.Workspace, name) {
				return "skill " + name + " is disabled for this workspace", nil
			}
			sk, ok := set.Get(name)
			if !ok {
				return "no skill named " + in.Name + " in the library", nil
			}
			body := sk.Body
			if len(body) > 20000 {
				body = body[:20000] + "\n…（技能正文过长，已截断）"
			}
			return "## " + sk.Name + "\n\n" + body, nil
		})
	if err != nil {
		return nil
	}
	return []tool.BaseTool{load}
}

// reportTicket writes the report through the durable wrapper when it is on.
func (r *Runtime) reportTicket(id domain.ID, state domain.ReportState, summary string) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if err := r.eng.Report(id, state, summary); err != nil {
		return err
	}
	if p != nil {
		// The mutation happened in memory; commit the Ticket so a restart keeps it.
		return p.SaveTicket(id)
	}
	return nil
}

// changedFile is one file the Worker touched.
type changedFile struct {
	Path  string
	Mtime time.Time
	Size  int64
}

// scanChanged lists files under root that are newer than since, skipping the
// runtime's own bookkeeping. It is how a run's output becomes deliverables even
// when the model forgets to call present: the panel should not depend on the
// model remembering (design §6.10).
func scanChanged(root string, since time.Time) []changedFile {
	var out []changedFile
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(rel, ".zeokeel") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(since) {
			return nil
		}
		out = append(out, changedFile{Path: rel, Mtime: info.ModTime(), Size: info.Size()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime.After(out[j].Mtime) })
	return out
}

// presentChanged turns a run's touched files into artifacts, newest first. It
// dedupes through the derived artifact id, so re-running after a recovery does not
// double the cards.
func (r *Runtime) presentChanged(t domain.Ticket, root string, since time.Time) {
	for _, f := range scanChanged(root, since) {
		kind := domain.ArtifactFile
		switch strings.ToLower(filepath.Ext(f.Path)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg":
			kind = domain.ArtifactImage
		case ".md", ".txt":
			kind = domain.ArtifactReport
		}
		detail := fmt.Sprintf("%s · %s", humanBytes(f.Size), f.Mtime.Format("15:04"))
		if _, err := r.eng.Present(t.ID, domain.Artifact{
			Kind: kind, Title: f.Path, Detail: detail, Path: f.Path,
		}); err == nil {
			// The panel saw the file appear because the runtime noticed it, not
			// because the model asked; say which, so the stream stays truthful.
			r.log.append(string(t.ID), workerEvent{kind: agent.EventToolEnd,
				tool: "present", args: fmt.Sprintf(`{"title":%q,"kind":%q}`, f.Path, kind)})
		}
	}
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

// capabilityTools are the agent-facing doors to capabilities that would otherwise
// only exist in code: timers and long-running objectives. Without them the agent
// can do a task but cannot say "do this every morning" (design §5.10).
func (r *Runtime) capabilityTools() []tool.BaseTool {
	r.mu.Lock()
	hasSched, hasGoals := r.sched != nil, r.goals != nil
	r.mu.Unlock()
	var out []tool.BaseTool

	if hasSched {
		t, err := utils.InferTool("schedule_task",
			"Create a recurring or one-shot task. Use it when the person asks for something to happen on a schedule.",
			func(_ context.Context, in struct {
				ID        string `json:"id" jsonschema:"description=short stable id, e.g. daily-usage-report"`
				Task      string `json:"task" jsonschema:"description=what to do when it fires"`
				When      string `json:"when" jsonschema:"description=每天 09:30 | 每 2 小时 | cron 0 9 * * 1-5"`
				Workspace string `json:"workspace,omitempty" jsonschema:"description=workspace id, empty means global"`
			}) (string, error) {
				def, err := r.ScheduleTask(in.ID, in.Task, in.When, domain.WorkspaceID(in.Workspace))
				if err != nil {
					return "", err
				}
				r.Publish()
				return "已创建定时任务 " + def.ID + "，下次 " + def.NextAt.Format("2006-01-02 15:04"), nil
			})
		if err == nil {
			out = append(out, t)
		}
	}

	if hasGoals {
		t, err := utils.InferTool("set_goal",
			"Record a durable objective to keep working toward across rounds.",
			func(_ context.Context, in struct {
				Objective string `json:"objective" jsonschema:"description=the outcome to reach, stated as a result"`
				Rounds    int    `json:"rounds,omitempty" jsonschema:"description=max rounds before giving up, default 20"`
				Workspace string `json:"workspace,omitempty" jsonschema:"description=workspace id"`
			}) (string, error) {
				rounds := in.Rounds
				if rounds <= 0 {
					rounds = 20
				}
				g, err := r.CreateGoal(domain.WorkspaceID(in.Workspace), in.Objective, rounds)
				if err != nil {
					return "", err
				}
				r.Publish()
				return "已记录目标 " + g.ID + "（最多 " + strconv.Itoa(g.MaxRounds) + " 轮）", nil
			})
		if err == nil {
			out = append(out, t)
		}
	}
	return out
}

// askQuestion records a question on the Ticket and puts it in the operator's to-do.
// This is the tool the live model was missing: without it a Worker could only write
// a sentence nobody was asked to answer (design §4.10).
func (r *Runtime) askQuestion(id domain.ID, question string, options []string) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if p != nil {
		// Persistence.RaiseAttention records the question and saves the Ticket in
		// one commit; the bare engine call + SaveTicket left a window in between.
		if err := p.RaiseAttention(id, question, options); err != nil {
			return err
		}
	} else if err := r.eng.RaiseAttention(id, question, options); err != nil {
		return err
	}
	r.log.append(string(id), workerEvent{kind: agent.EventToolEnd, tool: "ask_user_question",
		args: fmt.Sprintf(`{"question":%q}`, question)})
	r.Publish()
	return nil
}
