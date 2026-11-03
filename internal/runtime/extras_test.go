package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/goal"
	"github.com/zeokeel/super-agent/internal/schedule"
	"github.com/zeokeel/super-agent/internal/skills"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

func extrasRuntime(t *testing.T) (*Runtime, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	m, err := schedule.New(st)
	if err != nil {
		t.Fatal(err)
	}
	g, err := goal.New(st)
	if err != nil {
		t.Fatal(err)
	}
	rt.EnableSchedules(m)
	rt.EnableGoals(g)
	return rt, st
}

// A person says "每天早上九点报一次" in chat. The Worker has to be able to turn
// that into a real timer, not just acknowledge it (design §5.10).
func TestScheduleTaskParsesWhatPeopleSay(t *testing.T) {
	rt, st := extrasRuntime(t)
	defer st.Close()

	cases := map[string]string{
		"每天 09:30":         "09:30",
		"每 2 小时":           "2 小时",
		"cron 0 9 * * 1-5": "工作日九点",
	}
	i := 0
	for when, task := range cases {
		i++
		id := "job-" + string(rune('a'+i))
		def, err := rt.ScheduleTask(id, task, when, "")
		if err != nil {
			t.Fatalf("%q: %v", when, err)
		}
		if !def.Enabled || def.NextAt.IsZero() {
			t.Fatalf("%q produced %+v", when, def)
		}
	}
}

// A phrase we cannot read must be refused: guessing a time causes work at the
// wrong hour, which is worse than asking again.
func TestScheduleTaskRefusesUnknownTimes(t *testing.T) {
	rt, st := extrasRuntime(t)
	defer st.Close()
	for _, when := range []string{"晚点", "", "每天 25:00", "每 0 小时"} {
		if _, err := rt.ScheduleTask("job-x", "task", when, ""); err == nil {
			t.Fatalf("%q should be refused", when)
		}
	}
}

func TestGoalCanBeCreatedFromTheWorker(t *testing.T) {
	rt, st := extrasRuntime(t)
	defer st.Close()
	g, err := rt.CreateGoal("ws_web", "把构建时间降到 5 分钟以内", 12)
	if err != nil {
		t.Fatal(err)
	}
	if g.State != goal.Active || g.MaxRounds != 12 {
		t.Fatalf("goal %+v", g)
	}
	list, err := rt.Goals()
	if err != nil || len(list) != 1 {
		t.Fatalf("goals %v %v", list, err)
	}
}

// The tools only appear when the capability is on: an agent should not be told
// about a door it cannot open.
func TestCapabilityToolsFollowTheirManager(t *testing.T) {
	plain := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	if got := plain.capabilityTools(); len(got) != 0 {
		t.Fatalf("expected no capability tools, got %d", len(got))
	}
	rt, st := extrasRuntime(t)
	defer st.Close()
	names := toolNames(t, rt.capabilityTools())
	for _, want := range []string{"schedule_task", "set_goal"} {
		if !contains(names, want) {
			t.Fatalf("missing %q in %v", want, names)
		}
	}
}

type toolTool = tool.BaseTool

func contextBackground() context.Context { return context.Background() }

func toolNames(t *testing.T, tools []toolTool) []string {
	t.Helper()
	var out []string
	for _, tl := range tools {
		info, err := tl.Info(contextBackground())
		if err == nil && info != nil {
			out = append(out, info.Name)
		}
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

var _ = time.Now

// The agent itself must be able to schedule future work: that is the difference
// between "does a task" and "runs my morning routine" (design §5.10).
func TestWorkerCreatesAScheduleThroughItsTool(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "schedule_task"
	srv.ToolArgs = `{"id":"daily-usage","task":"整理 acme 的用量摘要","when":"每天 09:30"}`

	rt, st := extrasRuntime(t)
	defer st.Close()
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: t.TempDir(),
	})
	settleRuntime(t, rt)
	eng := rt.eng
	eng.Register(domain.Workspace{
		ID: "ws_acme", Name: "acme-data", Root: t.TempDir(), Kind: domain.KindData,
		Brief: domain.Brief{Purpose: "acme 用量", Keywords: []string{"acme"}},
	}, engine.Policy{Version: "v1"})
	if err := rt.Dispatch("在 acme 数据里建一个每天早上的用量摘要任务"); err != nil {
		t.Fatal(err)
	}

	m := rt.sched
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		list, err := m.List()
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range list {
			if d.ID == "daily-usage" {
				rt.WaitIdle(20 * time.Second)
				if !d.Enabled || d.NextAt.IsZero() {
					t.Fatalf("schedule created but unusable: %+v", d)
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the Worker never created the schedule")
}

// The Worker must be able to put a question in front of the operator. Before this
// tool existed a live model asked its question in prose and then reported "ready",
// which left nobody aware they were being asked anything.
func TestWorkerCanAskTheOperatorAQuestion(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	if _, err := eng.Dispatch(engine.DispatchInput{Text: "在 webapp 里改点东西", Workspace: "ws_web"}); err != nil {
		t.Fatal(err)
	}
	out := evalNow(eng)
	rt := New(eng, view.NewSnapshot())

	tools := rt.workerTools(mustTicket(t, eng, out.Ticket))
	var ask tool.BaseTool
	for _, tl := range tools {
		if info, err := tl.Info(contextBackground()); err == nil && info.Name == "ask_user_question" {
			ask = tl
		}
	}
	if ask == nil {
		t.Fatal("ask_user_question tool is missing")
	}
	inv, ok := ask.(interface {
		InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error)
	})
	if !ok {
		t.Fatal("tool is not invokable")
	}
	if _, err := inv.InvokableRun(contextBackground(),
		`{"question":"配置项叫 session_timeout 还是 session.timeout？","options":["session_timeout","session.timeout"]}`); err != nil {
		t.Fatal(err)
	}

	tk, _ := eng.TicketSnapshot(out.Ticket)
	if tk.Attention == nil || !strings.Contains(tk.Attention.Question, "session_timeout") {
		t.Fatalf("the question was not recorded: %+v", tk.Attention)
	}
	if got := domain.StageOf(&tk, time.Now()); got != domain.StagePersonAnswer {
		t.Fatalf("stage = %q, want person/answer", got)
	}
	rt.Publish()
	listed := false
	for _, row := range rt.snap.Overview().NeedsPerson {
		if row.ID == string(tk.ID) {
			listed = true
		}
	}
	if !listed {
		t.Fatal("the question did not reach the operator's to-do list")
	}
}

func mustTicket(t *testing.T, e *engine.Engine, id domain.ID) domain.Ticket {
	t.Helper()
	tk, ok := e.TicketSnapshot(id)
	if !ok {
		t.Fatalf("no ticket %q", id)
	}
	return tk
}

// The skills prompt tells the model it can load a skill by name; that promise needs
// a tool behind it, or the prompt is lying (found while reviewing prompts).
func TestLoadSkillToolExistsWhenSkillsAreConfigured(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "go-tests")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(
		"---\nname: go-tests\ndescription: Go 测试清单\nwhen-to-use: 测试\n---\n1. 先跑 go test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := skills.Load(dir, skills.Options{})
	if err != nil {
		t.Fatal(err)
	}

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: t.TempDir(),
		Kind: domain.KindRepo, Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}}},
		engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{BaseURL: "http://x", Model: "m", Root: t.TempDir(), Skills: set})
	settleRuntime(t, rt)
	if _, err := eng.Dispatch(engine.DispatchInput{Text: "在 webapp 里跑测试", Workspace: "ws_web"}); err != nil {
		t.Fatal(err)
	}
	out := evalNow(eng)
	tools := rt.workerTools(mustTicket(t, eng, out.Ticket))
	var load tool.BaseTool
	for _, tl := range tools {
		if info, err := tl.Info(contextBackground()); err == nil && info.Name == "load_skill" {
			load = tl
		}
	}
	if load == nil {
		t.Fatal("load_skill is missing while a skill library is configured")
	}
	inv, ok := load.(interface {
		InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error)
	})
	if !ok {
		t.Fatal("tool is not invokable")
	}
	body, err := inv.InvokableRun(contextBackground(), `{"name":"go-tests"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "先跑 go test") {
		t.Fatalf("the skill body was not returned: %q", body)
	}
}

// Without a library the tool must not appear: an agent should not be offered a door
// with nothing behind it.
func TestNoLoadSkillToolWithoutLibrary(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: t.TempDir(),
		Kind: domain.KindRepo, Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}}},
		engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{BaseURL: "http://x", Model: "m", Root: t.TempDir()})
	settleRuntime(t, rt)
	if _, err := eng.Dispatch(engine.DispatchInput{Text: "在 webapp 里跑测试", Workspace: "ws_web"}); err != nil {
		t.Fatal(err)
	}
	out := evalNow(eng)
	for _, tl := range rt.workerTools(mustTicket(t, eng, out.Ticket)) {
		if info, err := tl.Info(contextBackground()); err == nil && info.Name == "load_skill" {
			t.Fatal("load_skill appeared without a skill library")
		}
	}
}
