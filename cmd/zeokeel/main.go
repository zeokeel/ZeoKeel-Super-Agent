// Command zeokeel is the ZeoKeel Super Agent desktop app.
//
//	zeokeel            open the native window (closes to the tray; the runtime keeps running)
//	zeokeel -demo      run the domain walkthrough with no window, for CI and for reading the design
//	zeokeel -headless  same as -demo, for a box without a display
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/brief"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/ui"
	"github.com/zeokeel/super-agent/internal/view"
)

// version is injected at release time: -ldflags "-X main.version=1.0.0".
var version = ""

func main() { os.Exit(realMain()) }

// realMain returns the process exit code: 0 after a normal GUI/headless run, the
// subcommand's code otherwise (status uses the 006 contract, see runtime/status.go).
func realMain() int {
	// Subcommands are parsed before flags so `zeokeel status -json` and
	// `zeokeel -demo` cannot fight over the global FlagSet.
	if handled, code := runCLI(os.Args[1:]); handled {
		return code
	}
	var (
		demo         = flag.Bool("demo", false, "run the domain walkthrough without a window")
		headless     = flag.Bool("headless", false, "run the core as a service (no window; SIGINT/SIGTERM drains, exit 0/1/2/3)")
		width        = flag.Float64("width", 1440, "window width")
		height       = flag.Float64("height", 900, "window height")
		page         = flag.String("page", "", "open a specific page: overview, messages, source, trigger, eval, settings")
		model        = flag.String("model", "", "model id for the Worker; empty disables the executor")
		baseURL      = flag.String("base-url", "", "OpenAI-compatible endpoint for the Worker")
		apiKey       = flag.String("api-key", "", "API key for the Worker (prefer the keychain in a real deployment)")
		demoAI       = flag.Bool("demo-model", false, "run a built-in mock model so the Worker can be seen without a key")
		repo         = flag.String("repo", "", "watch a GitHub repository, owner/name (needs gh logged in)")
		ghLogin      = flag.String("gh-login", "", "the agent's own GitHub login for that repository")
		ghCLI        = flag.String("gh-cli", "gh", "the gh command or a wrapper script")
		stateDir     = flag.String("state-dir", defaultStateDir(), "where the database and session logs live")
		inMemory     = flag.Bool("in-memory", false, "keep state in memory only (demo and tests)")
		assessModel  = flag.String("assess-model", "", "model used by Eval; empty uses the deterministic rule assessor")
		skillsDir    = flag.String("skills", "", "directory of SKILL.md libraries to inject into Workers")
		mcpFile      = flag.String("mcp", "", "JSON file listing MCP servers to expose as tools")
		noSchedule   = flag.Bool("no-schedule", false, "do not run timers in this process")
		noSubagents  = flag.Bool("no-subagents", false, "do not give Workers nested investigator agents")
		feishuAppID  = flag.String("feishu-app-id", "", "Feishu app id; empty disables the Feishu listener")
		feishuSecret = flag.String("feishu-app-secret", "", "Feishu app secret (prefer saved settings)")
		feishuChats  = flag.String("feishu-chats", "", "comma-separated Feishu chat ids to listen to")
		dingAppKey   = flag.String("dingtalk-app-key", "", "DingTalk app key; empty disables the DingTalk listener")
		dingSecret   = flag.String("dingtalk-app-secret", "", "DingTalk app secret")
		dingRobot    = flag.String("dingtalk-robot", "", "DingTalk robot code")
		imSelf       = flag.String("im-self", "", "the bot's own account id on IM (for @ detection)")
		seedData     = flag.Bool("seed", false, "insert the demo records (only for screenshots and walkthroughs)")
		pipeline     = flag.String("pipeline", "", "run a batch job described by this JSON file, then exit")
	)
	flag.Parse()

	if version != "" {
		ui.Version = version
	}

	// Starting the app by hand is the person saying "run again": a previous
	// `zeokeel stop` marker is over.
	if !*inMemory {
		_ = runtime.ClearManuallyStopped(*stateDir)
	}
	// File logging: every mode that keeps running writes <stateDir>/logs/zeokeel.log.
	// Demo/pipeline keep stderr only, so `zeokeel -demo` never touches the disk.
	logger, closeLog := setupLogging(*stateDir, *headless || (!*demo && *pipeline == ""), *inMemory)
	defer closeLog()
	slog.SetDefault(logger)

	if *pipeline != "" {
		return runPipeline(*pipeline)
	}
	if *demo {
		runDemo()
		return 0
	}
	logger.Info("zeokeel starting",
		"version", version, "headless", *headless,
		"stateDir", *stateDir, "inMemory", *inMemory)

	// The mock model comes first: Eval needs it as much as the Worker does.
	worker := runtime.WorkerConfig{
		BaseURL: *baseURL, APIKey: *apiKey, Model: *model,
		// Under the state directory, so one -state-dir controls both the database
		// and the worktrees, and a fresh install starts clean.
		Root:    filepath.Join(*stateDir, "work"),
		Blocked: []string{"git push --force"},
	}
	if *demoAI {
		// A local OpenAI-compatible server, so the panel shows a real tool call and
		// a real assessment without a key. It never runs unless asked for.
		srv, err := mockllm.Start()
		if err != nil {
			fmt.Fprintln(os.Stderr, "zeokeel:", err)
			os.Exit(1)
		}
		defer srv.Close()
		srv.ToolName = "bash"
		srv.ToolArgs = `{"command":"echo worker-ran-this > hello.txt"}`
		worker.BaseURL, worker.APIKey, worker.Model = srv.URL, "sk-mock", "mock-tool"
		if *assessModel == "" {
			*assessModel = "mock-assess"
		}
	}
	// The store opens before the engine: a configured install keeps its model in
	// settings, and Eval has to be built with it (design §6.7).
	var early *store.Store
	saved := map[string]string{}
	if !*inMemory {
		if err := os.MkdirAll(*stateDir, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, "zeokeel: 无法创建状态目录:", err)
			return 1
		}
		st, err := store.Open(filepath.Join(*stateDir, "zeokeel.db"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "zeokeel: 无法打开状态目录:", err)
			return 1
		}
		early = st
		if m, err := st.Settings(); err == nil {
			saved = m
		}
	}
	// Command line first, then what the wizard saved.
	if worker.Model == "" || worker.BaseURL == "" {
		if mdl := saved["onboard.model"]; mdl != "" {
			worker.BaseURL = saved["onboard.base_url"]
			worker.APIKey = saved["onboard.api_key"]
			worker.Model = mdl
		}
	}
	if *assessModel == "" {
		*assessModel = saved["onboard.model"]
	}
	// The panel's Source settings are the fallback for the GitHub flags, so a
	// repository configured once persists across restarts.
	if *repo == "" {
		*repo = saved["source.github_repo"]
	}
	if *ghLogin == "" {
		*ghLogin = saved["source.github_login"]
	}
	imOpts := imOptions{
		FeishuAppID: *feishuAppID, FeishuAppSecret: *feishuSecret, FeishuChats: *feishuChats,
		DingAppKey: *dingAppKey, DingAppSecret: *dingSecret, DingRobot: *dingRobot,
		SelfID: *imSelf,
	}

	// One engine, one runtime, one snapshot. Everything the window shows is
	// projected from engine state through the same Dispatch path a person uses, so
	// nothing on screen is fabricated.
	eng := engine.New(newAssessor(*assessModel, worker.BaseURL, worker.APIKey, early))
	// One sender serves every Source: it looks the driver up by the message's
	// source, so a new driver only needs registering, not a new queue.
	sender := newSourceSender()
	var dur *durable
	if early != nil {
		d, err := openDurableOn(eng, early, sender)
		if err != nil {
			fmt.Fprintln(os.Stderr, "zeokeel: 无法打开状态目录:", err)
			return 1
		}
		dur = d
		defer dur.Close()
	}
	// Optional capabilities are loaded, not required: a bad config is reported and
	// the window still opens (design §5.10).
	ex, err := loadExtras(durStore(dur), *skillsDir, *mcpFile, logf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel: 扩展加载失败:", err)
	}
	if ex != nil && ex.closeFn != nil {
		defer ex.closeFn()
	}
	// Demo records only when asked for. A real install that was handed a model on
	// the command line must not be polluted with example conversations — that was a
	// live-run surprise, not a feature.
	if *seedData || *demoAI {
		seed(eng)
	}
	snap := view.NewSnapshot()
	rt := runtime.New(eng, snap)
	if err := attachDurable(rt, dur); err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel: 持久化通道启动失败:", err)
	}
	// A fresh install asks what the agent is for before it shows a dashboard; the
	// demo always comes pre-configured so the walkthrough has something to show.
	if !*inMemory && len(eng.Workspaces()) == 0 && !*demoAI {
		rt.SetNeedsOnboarding(true)
	} else {
		registerWorkspaces(eng, dur)
	}
	// Observe the workspaces once at startup: the Router's briefs are only as good
	// as the last scan, and a fresh install should not need a manual refresh.
	rt.RefreshWorkspaces()
	rt.Publish()

	// Republish on a timer: the projector stays dumb and the UI stays correct even
	// while the runtime grows event-driven publishing.
	// The 2s pass: advance the two-phase Eval (Receive only queues it) and
	// republish. This is what turns a source's Trigger into a Ticket/Decision
	// without blocking the entry path on a model call (fix-engine API §0.1).
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for range t.C {
			if _, err := rt.EvaluateDue(time.Now()); err != nil {
				logger.Error("eval pass failed", "error", err)
			}
			rt.Publish()
		}
	}()

	if ex != nil {
		// The capabilities become tools the Worker can actually call.
		rt.EnableSchedules(ex.sched)
		rt.EnableGoals(ex.goals)
	}
	if !*noSubagents {
		// Two bounded helpers by default: one reads, one runs. They cost a model
		// call only when the Worker decides to use them (design §5.6).
		worker.Subagents = runtime.DefaultSubagents()
	}
	if ex != nil {
		worker.Skills = ex.skills
		if ex.mcp != nil {
			worker.ExtraTools = append(worker.ExtraTools, ex.mcp.Tools()...)
		}
	}
	// The executor is always attached; whether it can run depends on having a
	// model. A fresh install gets one from the wizard without a restart.
	if !*inMemory {
		rt.SetStateDir(*stateDir)
	}
	rt.EnableWorker(worker)
	rt.SetVersion(version)
	// P0-A: model settings saved in the 模型 group take effect at startup too.
	rt.ApplyModelSettings()
	// A configured install records the marker `zeokeel status` reads. Status never
	// opens the engine, so this durable fact is how it tells 0 from 3.
	if len(eng.Workspaces()) > 0 && rt.HasWorkerModel() {
		if err := rt.MarkSetupComplete(); err != nil {
			logger.Warn("could not persist setup marker", "error", err)
		}
	}
	// Anything left "running" belongs to a process that is gone: recover it before
	// accepting new work (oldest first), so a crash resumes instead of wedging.
	if n := rt.RecoverAssignments(time.Now()); n > 0 {
		logger.Info("recovered interrupted assignments", "count", n)
	}
	if *demoAI {
		// One real assignment through the Worker, so the stream shows what the
		// executor does rather than only what the domain did.
		if err := rt.Dispatch("在 webapp 工作区里写一个 hello.txt，内容是 built by the worker"); err != nil {
			fmt.Fprintln(os.Stderr, "worker demo:", err)
		}
	}

	if *repo != "" && *ghLogin != "" {
		if err := startGitHub(rt, sender, githubOptions{
			Repo: *repo, Login: *ghLogin, CLI: *ghCLI, Poll: rt.PollInterval(),
		}); err != nil {
			// A source that cannot start is reported on the 来源 page, not fatal.
			rt.SetSourceStatus(runtime.SourceStatus{
				Kind: "github", Name: *repo, Account: *ghLogin, Enabled: false,
				Facts: []view.Fact{{Label: "仓库", Value: *repo}},
				Last: view.PollResult{At: time.Now(), Outcome: view.PollFailed,
					Problem: err.Error(), Detail: "Source 未能启动"},
			})
		}
	}

	// Headless is a real service: signals cancel the context, the loop drains and
	// the process exits with the 006 status code instead of opening a window.
	baseCtx := context.Background()
	if *headless {
		sigCtx, stopSig := signal.NotifyContext(baseCtx, os.Interrupt, syscall.SIGTERM)
		defer stopSig()
		baseCtx = sigCtx
	}
	// P0-4: flag-only IM deployments must be enabled before the manager starts;
	// explicit "0" still wins (migration is a no-op then).
	rt.BootstrapIMFlags(imOpts.flags())
	appCtx, stop := context.WithCancel(baseCtx)
	defer stop()
	// IM is opt-in: no credentials means no listener and no "online" row.
	imManager := startConfiguredIM(appCtx, rt, sender, imOpts, logger)
	defer imManager.Stop()
	if ex != nil && !*noSchedule {
		startScheduleLoop(appCtx, rt, ex.sched, 30*time.Second)
	}
	if *headless {
		return serveHeadless(appCtx, rt, logger)
	}

	if err := ui.Run(ui.Options{
		Title: "ZeoKeel", Width: float32(*width), Height: float32(*height),
		Snap: snap, Cmd: rt, StartSection: *page,
		// Let a running executor finish its current tool call before the process
		// goes away; the durable queue picks up anything that is still pending.
		Drain: rt.WaitIdle,
	}); err != nil {
		logger.Error("window failed", "error", err)
		fmt.Fprintln(os.Stderr, "zeokeel:", err)
		return 1
	}
	logger.Info("zeokeel stopped")
	return 0
}

// registerWorkspaces declares the workspaces this build serves. In durable mode
// they are committed through the persistence wrapper, so a restart finds them.
func registerWorkspaces(eng *engine.Engine, d *durable) {
	now := time.Now()
	register := func(w domain.Workspace, p engine.Policy) {
		if d != nil {
			if err := d.pers.Register(w, p); err != nil {
				fmt.Fprintln(os.Stderr, "zeokeel: 注册工作区失败:", err)
			}
			return
		}
		eng.Register(w, p)
	}
	register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: "~/code/webapp", Remote: "github.com/acme/webapp",
		Kind: domain.KindRepo,
		Brief: domain.Brief{
			Purpose:  "面向客户的 web 控制台：登录、账单、设置",
			Scope:    []string{"auth", "billing", "settings"},
			Keywords: []string{"webapp", "控制台", "登录", "账单"},
			Facts:    domain.Facts{ObservedAt: now, DefaultBranch: "main", Language: "TypeScript"},
		},
	}, engine.Policy{Version: "v1", Allowed: map[string]map[string]bool{
		"dingtalk": {"ding_zhangsan": true},
	}})
	register(domain.Workspace{
		ID: "ws_acme_data", Name: "acme-data", Root: "~/data/acme", Kind: domain.KindData,
		Brief: domain.Brief{
			Purpose:     "客户 acme 的用量数据、计费报表与对账",
			Scope:       []string{"acme", "用量", "计费", "对账", "月报"},
			Keywords:    []string{"acme", "ACME", "艾克米", "用量", "billing", "usage"},
			Constraints: []string{"只发汇总与趋势，不导出明细行", "对外必须经 operator 批准"},
			Facts: domain.Facts{ObservedAt: now, Language: "bash+sqlite",
				BuildCmd: "./scripts/query_usage.sh --month YYYY-MM"},
		},
	}, engine.Policy{Version: "v1", Allowed: map[string]map[string]bool{
		"dingtalk": {"ding_zhangsan": true},
	}})
}

// seed hands the engine three tasks through the person's own path, so the window
// opens on work that really exists. One of them raises the question a worker
// would ask, so the workbench shows how a pending decision looks.
func seed(e *engine.Engine) {
	var first domain.ID
	for i, t := range []struct{ text, ws string }{
		{"把登录超时改成可配置，默认 30 分钟", "ws_web"},
		{"给 session 配置补一个迁移脚本", "ws_web"},
		{"整理 acme 上月用量摘要", "ws_acme_data"},
	} {
		out, err := e.Dispatch(engine.DispatchInput{
			Text: t.text, Workspace: domain.WorkspaceID(t.ws), At: time.Now(),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "seed:", err)
			continue
		}
		if i == 0 {
			first = out.Ticket
		}
	}
	if first != "" {
		_ = e.RaiseAttention(first, "配置项名用 session_timeout 还是 session.timeout？",
			[]string{"session_timeout（推荐）", "session.timeout", "两者都支持"})
	}

	// The operator's own DingTalk: same channel, but it is them, so it is an
	// instruction like the panel's own input (§4.10).
	_, _ = e.Receive(domain.Trigger{
		ID:           domain.Key(domain.Global, "dingtalk", "seed-self"),
		Scope:        domain.Global,
		Source:       "dingtalk",
		Threads:      []domain.ThreadRef{{Source: "dingtalk", ID: "dm:me"}},
		Actor:        domain.Actor{ID: "ding_me"},
		Human:        true,
		Self:         true,
		Addressed:    domain.Addressed{Direct: true},
		Conversation: domain.Conversation{Kind: domain.ConvDirect, ID: "dm:me", Name: "我"},
		Text:         "把 acme 上周的报表也一起整理出来",
		At:           time.Now().Add(-90 * time.Second),
		Channel:      domain.ChannelRef{Source: "dingtalk", Thread: "dm:me"},
	})

	// A colleague asks in DingTalk. The agent may not answer for the operator
	// about customer data, so the stream shows it escalating instead (§4.10).
	_, _ = e.Receive(domain.Trigger{
		ID:           domain.Key(domain.Global, "dingtalk", "seed-1"),
		Scope:        domain.Global,
		Source:       "dingtalk",
		Threads:      []domain.ThreadRef{{Source: "dingtalk", ID: "dm:ding_zhangsan"}},
		Actor:        domain.Actor{ID: "ding_zhangsan"},
		Human:        true,
		Addressed:    domain.Addressed{Direct: true},
		Conversation: domain.Conversation{Kind: domain.ConvDirect, ID: "dm:ding_zhangsan", Name: "张三"},
		Text:         "帮我把 acme 这个客户上个月的用量数据发我一下",
		At:           time.Now().Add(-3 * time.Minute),
		Channel:      domain.ChannelRef{Source: "dingtalk", Thread: "dm:ding_zhangsan"},
	})
}

// runDemo walks the design §4.7 scenario end to end with no window: an IM message
// becomes a Trigger, the Router places it in a workspace, a Ticket opens, and the
// reply goes back to the conversation it came from.
func runDemo() {
	now := time.Now()
	e := engine.New(engine.RuleAssessor{Notices: []string{"今晚 8 点发版，注意别推代码"}})

	// A data workspace: not a repository. "The customer's data" lives here.
	e.Register(domain.Workspace{
		ID: "ws_acme_data", Name: "acme-data", Root: "~/data/acme", Kind: domain.KindData,
		Brief: domain.Brief{
			Purpose:  "客户 acme 的用量数据、计费报表与对账",
			Scope:    []string{"acme", "用量", "计费", "对账", "月报"},
			Keywords: []string{"acme", "ACME", "艾克米", "用量", "billing", "usage"},
			Aliases:  []string{"艾克米"},
			Constraints: []string{
				"只发汇总与趋势，不导出明细行",
				"对外必须经 operator 批准",
			},
			Facts: domain.Facts{ObservedAt: now, Language: "bash+sqlite",
				BuildCmd: "./scripts/query_usage.sh --month YYYY-MM"},
		},
	}, engine.Policy{Version: "v1", Allowed: map[string]map[string]bool{
		"dingtalk": {"ding_zhangsan": true},
	}})

	e.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: "~/code/webapp", Remote: "github.com/acme/webapp",
		Kind: domain.KindRepo,
		Brief: domain.Brief{
			Purpose:  "面向客户的 web 控制台：登录、账单、设置",
			Scope:    []string{"auth", "billing", "settings"},
			Keywords: []string{"webapp", "控制台", "登录", "账单"},
			Facts:    domain.Facts{ObservedAt: now, DefaultBranch: "main", Language: "TypeScript"},
		},
	}, engine.Policy{Version: "v1", Allowed: map[string]map[string]bool{
		"github": {"alice": true},
	}})

	fmt.Println("=== Router 上下文里的工作区简报（常驻，每区 ≤300 token）===")
	fmt.Print(brief.RenderAll(e.Workspaces(), now))

	transport := im.NewTransport()
	ding := im.DingTalk{T: transport}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan im.RawEvent, 4)
	go func() { _ = ding.Subscribe(ctx, events) }()
	time.Sleep(20 * time.Millisecond)

	fmt.Println("\n=== 同事在钉钉私聊 ===")
	ask := "帮我把 acme 这个客户上个月的用量数据发我一下"
	fmt.Println("张三：", ask)
	transport.Push(im.RawEvent{
		ID: "ding-msg-1", Actor: domain.Actor{ID: "ding_zhangsan"},
		Conv: domain.Conversation{Kind: domain.ConvDirect, ID: "dm:ding_zhangsan", Name: "张三"},
		Text: ask, At: now.UnixMilli(),
	})

	ev := <-events
	tr := translate("dingtalk", ev)
	fmt.Printf("\n① Trigger  scope=%s  id=%s\n", tr.Scope, short(string(tr.ID)))
	fmt.Printf("   事实：human=%v addressed{direct=%v} 会话=%s\n",
		tr.Human, tr.Addressed.Direct, tr.Conversation.Kind)

	out, err := e.Receive(tr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("② 准入：通过（allowed list 命中 dingtalk:ding_zhangsan）")
	fmt.Printf("③ 路由：关键词召回 → %s\n", out.Workspace)
	fmt.Printf("④ 决策：accept，Mandate 绑定到 %s（该区 Policy 签发）\n", out.Workspace)
	if tk, ok := e.TicketSnapshot(out.Ticket); ok {
		fmt.Printf("⑤ 派发：Ticket %s  阶段=%s\n", short(string(out.Ticket)), domain.StageOf(&tk, now))
	}

	fmt.Println("\n=== 数据敏感：第二道门 ===")
	_ = e.RaiseAttention(out.Ticket,
		"要把 acme 2026-10 用量摘要发给钉钉的张三吗？", []string{"允许一次", "拒绝"})
	if asked, ok := e.TicketSnapshot(out.Ticket); ok {
		fmt.Printf("ask_user_question → 阶段变为 %s（等 operator 批准）\n", domain.StageOf(&asked, now))
	}

	e.Reply(out.Ticket, "已找到 acme 的用量数据，摘要见附件；明细需要你确认后再发。")
	for _, r := range e.Replies() {
		_, _ = ding.Send(ctx, im.Outbound{Key: r.Key, Conv: tr.Conversation, ReplyTo: ev.ID, Text: r.Text})
	}
	fmt.Println("\n=== 回写 ===")
	for _, s := range transport.Sent() {
		fmt.Printf("钉钉 ← %q（幂等键 %s）\n", s.Text, short(s.Key))
	}

	fmt.Println("\n=== 反例：群里发公告 ===")
	notice := translate("dingtalk", im.RawEvent{
		ID: "ding-msg-2", Actor: domain.Actor{ID: "ding_zhangsan"},
		Conv:     domain.Conversation{Kind: domain.ConvGroup, ID: "group:ops", Name: "运维群"},
		Text:     "今晚 8 点发版，注意别推代码",
		At:       now.UnixMilli(),
		Mentions: []string{"ding_bot"},
	})
	notice.DefaultWorkspace = "ws_web"
	fmt.Println("@所有人：", notice.Text)
	nout, err := e.Receive(notice)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if nout.Ticket == "" {
		fmt.Printf("→ 没有开 Ticket；判定=%s 理由=%v\n", nout.Kind, reasonCodes(nout.Reasons))
	} else {
		fmt.Printf("→ 错误：公告也开了 Ticket %s\n", nout.Ticket)
	}

	fmt.Println("\n=== 反例：问的内容对不上任何工作区 ===")
	unknown := translate("dingtalk", im.RawEvent{
		ID: "ding-msg-3", Actor: domain.Actor{ID: "ding_zhangsan"},
		Conv: domain.Conversation{Kind: domain.ConvDirect, ID: "dm:ding_zhangsan"},
		Text: "今天中午吃什么", At: now.UnixMilli(),
	})
	uout, _ := e.Receive(unknown)
	if uout.NeedsPerson {
		fmt.Printf("→ 不猜，反问：%s\n", uout.Question)
	}
}

// translate turns a driver's raw event into a domain Trigger. Every IM driver
// uses this same mapping: conversation → Thread, message → Trigger.
func translate(source string, ev im.RawEvent) domain.Trigger {
	scope := domain.Global
	return domain.Trigger{
		ID:      domain.Key(scope, source, ev.ID),
		Scope:   scope,
		Source:  source,
		Threads: []domain.ThreadRef{{Source: source, ID: threadID(ev)}},
		Actor:   ev.Actor,
		Human:   !ev.Actor.Bot,
		Addressed: domain.Addressed{
			Mention: len(ev.Mentions) > 0,
			Direct:  ev.Conv.Kind == domain.ConvDirect,
			ReplyTo: ev.ReplyTo != "",
		},
		Conversation: ev.Conv,
		Text:         ev.Text,
		At:           time.UnixMilli(ev.At),
		Channel:      domain.ChannelRef{Source: source, Thread: threadID(ev)},
	}
}

func threadID(ev im.RawEvent) string {
	if ev.Conv.RootID != "" {
		return ev.Conv.ID + ":" + ev.Conv.RootID
	}
	return ev.Conv.ID
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func reasonCodes(rs []domain.Reason) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Code)
	}
	return out
}
