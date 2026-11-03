package main

// Subcommands are parsed before flags: `zeokeel status --json` must not go through
// the global FlagSet, and `zeokeel -demo` must keep working. The exit codes of
// `status` are the prototype's 006:113-124 contract (see internal/runtime/status.go).

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/store"
)

const cliUsage = `zeokeel — 单机 AI 同事

用法：
  zeokeel                    打开窗口（正常模式）
  zeokeel -headless          以服务模式运行（无窗口，信号退出，退出码 0/1/2/3）
  zeokeel -demo              领域 walkthrough（无窗口，无副作用）
  zeokeel -pipeline FILE     跑一个 JSON 批量流水线后退出

子命令：
  zeokeel status  [--json] [--state-dir DIR]   运行状态/健康/schema/setup；退出码 0/1/2/3
  zeokeel check   [--json] [--state-dir DIR]   启动前自检（目录/数据库/日志/配置）
  zeokeel setup   [--state-dir DIR] [--model M --base-url U --api-key K
                   --workspace NAME --root PATH --keywords K1,K2 --people src:actor]
                                               非交互完成首次配置；无参数时报告还缺什么
  zeokeel login   [--gh gh]                    检查 GitHub CLI 登录状态（IM 凭据按文档配置）
  zeokeel backup  [--state-dir DIR] [--out FILE]
                                               用 VACUUM INTO 做一致性备份
  zeokeel archive [--state-dir DIR] [--before 180d] [--dry-run]
                                               把结束超过 N 天的工单移到 archive.db

退出码（status 与 -headless）：0 正常 / 1 可重启修复 / 2 组件错误 / 3 等待 setup
`

var cliCommands = map[string]func([]string) int{
	"status":  cmdStatus,
	"check":   cmdCheck,
	"setup":   cmdSetup,
	"login":   cmdLogin,
	"backup":  cmdBackup,
	"archive": cmdArchive,
	"stop":    cmdStop,
}

// runCLI dispatches a subcommand. handled=false means "not a subcommand; fall
// through to the flag-based modes".
func runCLI(args []string) (handled bool, code int) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return false, 0
	}
	if args[0] == "help" {
		fmt.Print(cliUsage)
		return true, 0
	}
	fn, ok := cliCommands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "zeokeel: 未知子命令 %q\n\n%s", args[0], cliUsage)
		return true, 2
	}
	return true, fn(args[1:])
}

// cmdStatus prints the install's state and maps it to the 006 exit code.
func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	stateDir := fs.String("state-dir", defaultStateDir(), "where the state directory lives")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return runtime.ExitComponent
	}
	st := runtime.InspectStatus(*stateDir)
	code := st.ExitCode()
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(st)
		return code
	}
	printStatus(st, code)
	return code
}

func printStatus(st runtime.Status, code int) {
	fmt.Printf("目录      %s\n", st.StateDir)
	fmt.Printf("进程      %s\n", map[bool]string{true: "在运行", false: "没在跑"}[st.Running])
	schema := "未知"
	if st.SchemaKnown {
		schema = fmt.Sprintf("v%d（本构建 v%d）", st.SchemaVersion, st.BuildSchema)
	}
	fmt.Printf("数据库    %s\n", schema)
	fmt.Printf("负载      执行中 %d / 并发 %d · 队列 %d\n",
		st.Load.Running, st.Load.MaxWorkers, st.Load.Queued)
	if len(st.Health) > 0 {
		fmt.Println("健康：")
		for _, h := range st.Health {
			fmt.Printf("  [%s] %-14s %s %s\n", h.State, h.Part, h.Detail, "")
		}
	}
	if len(st.Errors) > 0 {
		fmt.Println("错误：")
		for _, e := range st.Errors {
			fmt.Printf("  %-14s %s\n", e.Part, e.Detail)
		}
	}
	if len(st.Setup) > 0 {
		fmt.Println("等待 setup：")
		for _, gap := range st.Setup {
			fmt.Printf("  - %s\n", gap)
		}
	}
	fmt.Printf("结论      %s（退出码 %d）\n", statusWord(code), code)
}

// statusWord is the human reading of the exit code, kept next to the code table.
func statusWord(code int) string {
	switch code {
	case runtime.ExitOK:
		return "正常"
	case runtime.ExitRestart:
		return "没在跑或运行时报错：可以重启修复"
	case runtime.ExitComponent:
		return "组件错误：不要自动重启"
	case runtime.ExitSetup:
		return "等待 setup：不要自动重启"
	}
	return "未知"
}

// cmdCheck is the preflight: it can run while the app is up, and it does not need
// the app at all. It reports what would stop the next start.
func cmdCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	stateDir := fs.String("state-dir", defaultStateDir(), "where the state directory lives")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return runtime.ExitComponent
	}
	st := runtime.InspectStatus(*stateDir)
	type checkRow struct {
		Part   string `json:"part"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	var rows []checkRow
	add := func(part string, ok bool, detail string) {
		rows = append(rows, checkRow{Part: part, OK: ok, Detail: detail})
	}
	add("状态目录", dirWritable(*stateDir), *stateDir)
	add("数据库", st.SchemaKnown && len(st.Errors) == 0,
		fmt.Sprintf("schema v%d", st.SchemaVersion))
	add("日志目录", dirWritable(filepath.Join(*stateDir, "logs")), filepath.Join(*stateDir, "logs"))
	add("模型", !strings.Contains(strings.Join(st.Setup, " "), "模型"), "onboard.model / 启动参数")
	add("setup", len(st.Setup) == 0, strings.Join(st.Setup, "；"))

	// Exit-code rule: a real component failure is 2; a fresh/partial install that
	// only needs setup is 3 (its missing DB/schema/model rows are not failures).
	code := runtime.ExitOK
	if len(st.Errors) > 0 {
		code = runtime.ExitComponent
	} else if len(st.Setup) > 0 {
		code = runtime.ExitSetup
	} else {
		for _, r := range rows {
			if !r.OK {
				code = runtime.ExitComponent
			}
		}
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"ok": code == runtime.ExitOK, "checks": rows, "exitCode": code})
		return code
	}
	for _, r := range rows {
		mark := "PASS"
		if !r.OK {
			mark = "FAIL"
		}
		fmt.Printf("%s  %-10s %s\n", mark, r.Part, r.Detail)
	}
	fmt.Printf("check: %s（退出码 %d）\n", statusWord(code), code)
	return code
}

// dirWritable proves the directory is usable by creating and removing one file.
// It is the only honest check: mode bits lie under ACLs and read-only mounts.
func dirWritable(dir string) bool {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// cmdSetup configures an install without a window, which is what a headless
// deployment needs. With no values it reports the gaps instead of guessing.
func cmdSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	stateDir := fs.String("state-dir", defaultStateDir(), "where the state directory lives")
	model := fs.String("model", "", "model id (e.g. deepseek-chat)")
	baseURL := fs.String("base-url", "", "OpenAI-compatible endpoint")
	apiKey := fs.String("api-key", "", "API key (stored in the local SQLite file)")
	wsName := fs.String("workspace", "", "workspace name")
	wsRoot := fs.String("root", "", "workspace root directory")
	wsPurpose := fs.String("purpose", "", "one line: what this workspace is for")
	wsKeywords := fs.String("keywords", "", "comma-separated routing keywords")
	people := fs.String("people", "", "allowlist: source:actor, source:actor")
	if err := fs.Parse(args); err != nil {
		return runtime.ExitComponent
	}
	st := runtime.InspectStatus(*stateDir)
	configured := *model == "" && *wsName == "" && *baseURL == "" && *apiKey == "" &&
		*wsRoot == "" && *wsPurpose == "" && *wsKeywords == "" && *people == ""
	if configured {
		if len(st.Setup) == 0 {
			fmt.Println("setup: 已完成，无需操作（zeokeel status 可确认）")
			return runtime.ExitOK
		}
		fmt.Println("setup: 还缺：")
		for _, gap := range st.Setup {
			fmt.Printf("  - %s\n", gap)
		}
		fmt.Println("非交互配置示例：")
		fmt.Println("  zeokeel setup --model deepseek-chat --base-url https://api.deepseek.com/v1 \\")
		fmt.Println("      --api-key sk-... --workspace webapp --root ~/code/webapp --keywords webapp")
		return runtime.ExitSetup
	}
	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel setup:", err)
		return runtime.ExitComponent
	}
	dbPath := filepath.Join(*stateDir, "zeokeel.db")
	stWriter, err := store.Open(dbPath)
	if err != nil {
		if errors.Is(err, store.ErrAlreadyRunning) {
			fmt.Fprintln(os.Stderr, "zeokeel setup: 应用正在运行；请在设置页修改，或先退出应用")
			return runtime.ExitRestart
		}
		fmt.Fprintln(os.Stderr, "zeokeel setup:", err)
		return runtime.ExitComponent
	}
	defer stWriter.Close()

	values := map[string]string{"onboard.done": "1"}
	if *model != "" {
		values["onboard.model"] = *model
		values["onboard.base_url"] = *baseURL
		values["onboard.api_key"] = *apiKey
	}
	if *wsName != "" {
		values["onboard.ws_name"] = *wsName
		values["onboard.ws_root"] = *wsRoot
		values["onboard.ws_purpose"] = *wsPurpose
		values["onboard.ws_keywords"] = *wsKeywords
	}
	if *people != "" {
		values["onboard.people"] = *people
	}
	if _, err := stWriter.SaveSettings(values, 0, false); err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel setup: 保存设置:", err)
		return runtime.ExitComponent
	}
	if *wsName != "" {
		if err := registerWorkspaceCLI(stWriter, *wsName, *wsRoot, *wsPurpose, *wsKeywords, *people); err != nil {
			fmt.Fprintln(os.Stderr, "zeokeel setup:", err)
			return runtime.ExitComponent
		}
	}
	if err := stWriter.Set("setup.complete", "1"); err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel setup:", err)
		return runtime.ExitComponent
	}
	fmt.Println("setup: 已保存。启动 `zeokeel -headless` 或打开应用即可。")
	return runtime.ExitOK
}

// registerWorkspaceCLI writes the workspace row and its policy. The engine reads
// both at boot; it is the same shape the wizard registers.
func registerWorkspaceCLI(st *store.Store, name, root, purpose, keywords, people string) error {
	if root == "" {
		root = "~/" + name
	}
	id := domain.WorkspaceID("ws_" + strings.ToLower(strings.ReplaceAll(name, " ", "-")))
	kw := []string{name}
	for _, k := range strings.Split(keywords, ",") {
		if k = strings.TrimSpace(k); k != "" {
			kw = append(kw, k)
		}
	}
	w := domain.Workspace{
		ID: id, Name: name, Root: root, Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: purpose, Keywords: kw},
	}
	if err := st.PutWorkspace(w); err != nil {
		return err
	}
	policy := engine.Policy{Version: "v1", Allowed: parseAllowlistCLI(people)}
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return st.PutWorkspacePolicy(id, string(raw))
}

// parseAllowlistCLI accepts "source:actor, source:actor". A malformed entry is an
// error: silently locking a colleague out is worse than failing setup.
func parseAllowlistCLI(raw string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}
		source, actor, ok := strings.Cut(entry, ":")
		source, actor = strings.TrimSpace(source), strings.TrimSpace(actor)
		if !ok || source == "" || actor == "" {
			continue
		}
		if out[source] == nil {
			out[source] = map[string]bool{}
		}
		out[source][actor] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// cmdStop records a deliberate stop: the next supervisor check must not restart
// the app (006 "manually stopped"), and if a pid is given the running process is
// asked to stop. There is no management HTTP API (desktop ADR), so a running app
// without a pid can only be stopped from its tray: saying that is better than
// pretending.
func cmdStop(args []string) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	stateDir := fs.String("state-dir", defaultStateDir(), "where the state directory lives")
	pid := fs.Int("pid", 0, "pid of the running zeokeel to signal (optional)")
	noSignal := fs.Bool("no-signal", false, "only write the stop marker")
	if err := fs.Parse(args); err != nil {
		return runtime.ExitComponent
	}
	if err := runtime.MarkManuallyStopped(*stateDir); err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel stop:", err)
		return runtime.ExitComponent
	}
	st := runtime.InspectStatus(*stateDir)
	fmt.Println("已标记为手动停止：监督脚本不会自动重启（下次启动会清除这个标记）。")
	if st.Running && !*noSignal {
		if *pid > 0 {
			if proc, err := os.FindProcess(*pid); err == nil {
				if err := proc.Signal(syscall.SIGTERM); err == nil {
					fmt.Printf("已向 pid %d 发送 SIGTERM，等待其优雅退出。\n", *pid)
				} else {
					fmt.Fprintf(os.Stderr, "zeokeel stop: 发送信号失败: %v\n", err)
					return runtime.ExitComponent
				}
			}
		} else {
			fmt.Println("应用正在运行：本构建没有管理 IPC，请用托盘「退出」停止它（标记已经写入）。")
		}
	}
	return runtime.ExitOK
}

// cmdLogin checks the GitHub CLI login the GitHub Source reuses (ADR: gh stores
// the credentials; we never do). IM credentials are environment/config values,
// so this command names them instead of pretending to verify them.
func cmdLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	gh := fs.String("gh", "gh", "the gh command or a wrapper")
	if err := fs.Parse(args); err != nil {
		return runtime.ExitComponent
	}
	if _, err := exec.LookPath(*gh); err != nil {
		fmt.Fprintf(os.Stderr, "zeokeel login: 没找到 %s；GitHub 来源需要 gh 已安装并登录\n", *gh)
		return runtime.ExitRestart
	}
	out, err := exec.Command(*gh, "auth", "status").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "zeokeel login: gh 未登录：%v\n%s\n运行 `%s auth login` 后重试。\n", err, out, *gh)
		return runtime.ExitRestart
	}
	fmt.Print(string(out))
	fmt.Println("GitHub: 已登录（Source 会复用这份登录态）。")
	fmt.Println("飞书/钉钉：凭据来自启动参数或环境变量，本命令不读取、不打印它们。")
	return runtime.ExitOK
}
