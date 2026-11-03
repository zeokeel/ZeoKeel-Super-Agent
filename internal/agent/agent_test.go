package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
)

// startMock 起一个 OpenAI 兼容的 mock 服务，测试不依赖任何真实 API key。
func startMock(t *testing.T) *mockllm.Server {
	t.Helper()
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// testConfig 是跑 mock 模型的默认配置；opts 用来覆盖单个测试关心的项。
func testConfig(srv *mockllm.Server, workdir, model string, opts ...func(*Config)) Config {
	cfg := Config{
		BaseURL: srv.URL,
		APIKey:  "sk-mock",
		Model:   model,
		Workdir: workdir,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func newTestRunner(t *testing.T, cfg Config) *Runner {
	t.Helper()
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// runSessionForTest 用生产路径（RunSession）跑一轮，避免任何测试再依赖已删除的
// 无会话 Runner.Run。
func runSessionForTest(t *testing.T, r *Runner, task string, emit func(Event)) RunResult {
	t.Helper()
	res, err := r.RunSession(context.Background(), RunRequest{System: "sys", Task: task}, emit)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// echoTool 顶上真实工具，让 mock 模型发出的 tool_call 能解析到实现。
func echoTool() tool.BaseTool {
	t, err := utils.InferTool("echo_upper", "Upper-case the given text.",
		func(_ context.Context, in struct {
			Text string `json:"text"`
		}) (struct {
			Upper string `json:"upper"`
			Len   int    `json:"len"`
		}, error) {
			return struct {
				Upper string `json:"upper"`
				Len   int    `json:"len"`
			}{strings.ToUpper(in.Text), len(in.Text)}, nil
		})
	if err != nil {
		panic(err)
	}
	return t
}

// 会话路径必须真的执行模型要的工具并把结果喂回去，而不是在第一个模型轮就停。
// mock-tool 会先流式输出一句话、再发 tool_call——正好是 eino 默认检查器会丢掉
// 的那种形态（旧的 bufferToolCallChecker 那条 react 路径已删除，见 F1）。
func TestRunExecutesToolAndFinishes(t *testing.T) {
	srv := startMock(t)
	r := newTestRunner(t, testConfig(srv, t.TempDir(), "mock-tool",
		func(c *Config) { c.ExtraTools = []tool.BaseTool{echoTool()} }))

	var sawStart, sawEnd, sawDone, sawUsage bool
	var final string
	res := runSessionForTest(t, r, "echo hello", func(e Event) {
		switch e.Kind {
		case EventToolStart:
			sawStart = true
			if e.Tool != "echo_upper" {
				t.Errorf("tool %q", e.Tool)
			}
		case EventToolEnd:
			sawEnd = true
			if !strings.Contains(e.Output, "HELLO") {
				t.Errorf("tool output %q", e.Output)
			}
		case EventUsage:
			sawUsage = true
		case EventDone:
			sawDone = true
		}
	})
	final = res.Answer
	if !sawStart || !sawEnd {
		t.Fatalf("tool lifecycle missing: start=%v end=%v", sawStart, sawEnd)
	}
	if !sawDone {
		t.Fatal("no done event")
	}
	if !sawUsage {
		t.Fatal("no usage event")
	}
	if !strings.Contains(final, "TOOL_RESULT_SEEN") {
		t.Fatalf("the model never saw the tool result: %q", final)
	}
	// 模型被调用了两次：一次决定用工具，一次看到结果后收尾。只调用一次说明工具
	// 闭环断了。
	if got := srv.RequestCount(); got != 2 {
		t.Fatalf("want 2 model calls (tool round trip), got %d", got)
	}
}

// 路径不能逃出工作区：必须是 refusal（词法上的越界根本不会到 os.Root）。
func TestWorkspaceRefusesLexicalEscape(t *testing.T) {
	root := t.TempDir()
	backend := &localBackend{root: root}
	bad := []string{
		"../../etc/passwd",
		"/etc/passwd",
		"a/../../escape.txt",
		"..",
	}
	for _, p := range bad {
		if _, err := backend.rootPath(p); err == nil {
			t.Fatalf("path %q outside the workspace must be refused", p)
		}
	}
	for _, p := range []string{"ok/file.txt", "sub/../ok/file.txt", "."} {
		if _, err := backend.rootPath(p); err != nil {
			t.Fatalf("path %q inside the workspace was refused: %v", p, err)
		}
	}
	// 工作区内的绝对路径被接受（模型常把技能里的绝对路径原样带过来）。
	if _, err := backend.rootPath(filepath.Join(root, "abs.txt")); err != nil {
		t.Fatalf("an absolute path inside the workspace was refused: %v", err)
	}
}

// 被 block 的命令按整条命令的前缀拒绝（F22：旧实现只比对 firstWord，多词条目
// 永远匹配不到）。
func TestBlockedCommandIsRefused(t *testing.T) {
	blocked := newBlockedCommands([]string{"git push --force", "rm", "git"})
	cfg := Config{Workdir: t.TempDir()}
	res, err := runBash(context.Background(), cfg.Workdir, blocked, 0, nil,
		BashArgs{Command: "git   push --force origin main"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 126 || !strings.Contains(res.Output, "refused") {
		t.Fatalf("expected a refusal for a multi-word blocked command, got %+v", res)
	}
	// 单词"git"条目还在，legacy 配置继续有效。
	res, err = runBash(context.Background(), cfg.Workdir, blocked, 0, nil, BashArgs{Command: "git status"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 126 || !strings.Contains(res.Output, "git") {
		t.Fatalf("legacy single-word blocked entry stopped working: %+v", res)
	}
	// 默认配置只 block "git push --force"：git status 必须放行。
	res, err = runBash(context.Background(), cfg.Workdir, newBlockedCommands([]string{"git push --force"}),
		0, nil, BashArgs{Command: "git status --short"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode == 126 || strings.Contains(res.Output, "refused") {
		t.Fatalf("git status was blocked by a push-only entry: %+v", res)
	}
	// 未被 block 的命令正常执行。
	res, err = runBash(context.Background(), cfg.Workdir, newBlockedCommands([]string{"git push --force"}),
		0, nil, BashArgs{Command: "echo ok"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Output, "ok") {
		t.Fatalf("an allowed command was blocked: %+v", res)
	}
}

// glob 必须兑现描述里的 **（F2）：旧实现用 filepath.Glob，** 等价于 *，三层目录
// 匹配不到。这条测试走的是官方 filesystem 工具 + 本地 backend 的真实调用路径。
func TestGlobMatchesDoubleStar(t *testing.T) {
	dir := t.TempDir()
	files := []string{"a/b/c/z.go", "a/x.go", "internal/x.go", "internal/y/x.go", "readme.md"}
	for _, name := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	backend := &localBackend{root: dir}
	ctx := context.Background()
	got, err := backend.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, fi := range got {
		names[fi.Path] = true
	}
	for _, want := range []string{"a/b/c/z.go", "a/x.go", "internal/x.go", "internal/y/x.go"} {
		if !names[want] {
			t.Fatalf("**/*.go did not match %q (got %v)", want, names)
		}
	}
	if names["readme.md"] {
		t.Fatalf("**/*.go matched a non-Go file: %v", names)
	}
	got, err = backend.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: "internal/**/x.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("internal/**/x.go = %v, want internal/x.go and internal/y/x.go", got)
	}
}

// 工作区内指向工作区外的符号链接必须被挡住（F3：旧的 inside() 只做词法检查）。
// os.Root 把这类链接解析错误交给调用方，read/write 都不能逃逸。
func TestSymlinkEscapeIsRefused(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	// os.Root 只跟随相对符号链接，所以逃逸链接本身用相对目标构造。
	relSecret, err := filepath.Rel(root, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relSecret, filepath.Join(root, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	relOutside, err := filepath.Rel(root, outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relOutside, filepath.Join(root, "leakdir")); err != nil {
		t.Fatal(err)
	}
	backend := &localBackend{root: root}
	ctx := context.Background()
	if _, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: "leak.txt"}); err == nil {
		t.Fatal("reading a symlink that points outside the workspace must fail")
	} else if msg, ok := toolErrorObservation(err); !ok || !strings.Contains(msg, "escape") {
		t.Fatalf("the escape error was not classified as a refusal observation: %v", err)
	}
	if err := backend.Write(ctx, &filesystem.WriteRequest{
		FilePath: "leakdir/planted.txt", Content: "x",
	}); err == nil {
		t.Fatal("writing through a symlinked directory that points outside must fail")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "planted.txt")); statErr == nil {
		t.Fatal("the file was written outside the workspace through a symlink")
	}
	// 工作区内部的正常符号链接仍然可用（不是把所有链接都禁掉）。
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(root, "inside-link.txt")); err != nil {
		t.Fatal(err)
	}
	fc, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: "inside-link.txt"})
	if err != nil {
		t.Fatalf("an inside-workspace symlink must keep working: %v", err)
	}
	if fc.Content != "inside" {
		t.Fatalf("inside symlink read = %q", fc.Content)
	}
}

// 会话目录必须落在工作区里（默认），否则同一台机器上多个工作区会互相覆盖事件日志。
func TestSessionDirDefaultsInsideWorkdir(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-text"))
	if got, want := r.Config().SessionDir, filepath.Join(dir, ".zeokeel", "sessions"); got != want {
		t.Fatalf("session dir = %q, want %q", got, want)
	}
}

// 事件序号在单次运行内单调递增；面板靠它排序（回调与事件循环是两侧写入）。
func TestEventSeqIsMonotonic(t *testing.T) {
	srv := startMock(t)
	r := newTestRunner(t, testConfig(srv, t.TempDir(), "mock-tool",
		func(c *Config) { c.ExtraTools = []tool.BaseTool{echoTool()} }))
	var last uint64
	runSessionForTest(t, r, "echo hello", func(e Event) {
		if e.Seq <= last {
			t.Fatalf("seq not monotonic: %d after %d (kind=%s)", e.Seq, last, e.Kind)
		}
		last = e.Seq
	})
}

// 面板的 Live Transcript 必须边生成边出字：文本事件要显著早于运行结束，
// 而不是等整轮模型输出结束再一次性刷出来（design §5.7）。
func TestTextEventsStreamBeforeRunEnds(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-slow"))
	start := time.Now()
	var firstText time.Duration
	err := func() error {
		_, err := r.RunSession(context.Background(), RunRequest{System: "sys", Task: "讲个长答案"}, func(e Event) {
			if e.Kind == EventText && firstText == 0 {
				firstText = time.Since(start)
			}
		})
		return err
	}()
	end := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if firstText == 0 {
		t.Fatal("no text events")
	}
	if end-firstText < time.Second {
		t.Fatalf("text was buffered until the end: first text at %s, run ended at %s", firstText, end)
	}
}

// A model that asks for a path outside the workspace gets told "no" as a tool
// observation, not as a run failure: it can correct itself and finish the task.
// Only infrastructure problems should end a run (design §5.5 + §5.8).
func TestPathEscapeIsAnObservationNotAFailure(t *testing.T) {
	srv := startMock(t)
	srv.ToolName = "write"
	srv.ToolArgs = `{"file_path":"../escape.txt","content":"x"}`
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-tool"))

	res := runSessionForTest(t, r, "写一个文件到工作区外", nil)
	if !strings.Contains(res.Answer, "refused") || !strings.Contains(res.Answer, "outside the workspace") {
		t.Fatalf("the model did not get a refusal observation: %q", res.Answer)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "..", "escape.txt")); statErr == nil {
		t.Fatal("the file was written outside the workspace")
	}
}
