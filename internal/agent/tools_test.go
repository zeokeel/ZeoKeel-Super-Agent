package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

// read 的 offset/limit 语义通过官方 read_file 工具走完整链路：模型看到的是带行号的
// 区间，offset 越界是文本 observation 而不是整轮失败（F4/F6 的行为约束）。
func TestReadOffsetLimitThroughTool(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"),
		[]byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.ToolName = "read"
	srv.ToolArgs = `{"file_path":"f.txt","offset":2,"limit":2}`
	r := newTestRunner(t, testConfig(srv, dir, "mock-tool"))

	res := runSessionForTest(t, r, "读第二三行", nil)
	answer := res.Answer
	if !strings.Contains(answer, "two") || !strings.Contains(answer, "three") {
		t.Fatalf("offset/limit did not return lines 2-3: %q", answer)
	}
	if strings.Contains(answer, "four") {
		t.Fatalf("limit was ignored, line 4 leaked in: %q", answer)
	}
	if !strings.Contains(answer, "2\ttwo") || !strings.Contains(answer, "3\tthree") {
		t.Fatalf("read output is not line-numbered: %q", answer)
	}
}

// offset 越过末行必须是模型可读的提示，而不是让整轮失败（旧实现返回 Go error）。
func TestReadPastEndIsAnObservation(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.ToolName = "read"
	srv.ToolArgs = `{"file_path":"f.txt","offset":99}`
	r := newTestRunner(t, testConfig(srv, dir, "mock-tool"))

	res := runSessionForTest(t, r, "从第 99 行读", nil)
	if !strings.Contains(res.Answer, "past its last line") {
		t.Fatalf("reading past the end did not produce an observation: %q", res.Answer)
	}
}

// edit 命中多次是 refusal observation，文件保持原样；旧实现返回 Go error 会让
// 整个 Ticket 失败（F6/§3.3）。
func TestEditMultipleMatchesIsAnObservation(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "e.txt")
	if err := os.WriteFile(path, []byte("dup\ndup\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.ToolName = "edit"
	srv.ToolArgs = `{"file_path":"e.txt","old_string":"dup","new_string":"x"}`
	r := newTestRunner(t, testConfig(srv, dir, "mock-tool"))

	res := runSessionForTest(t, r, "把 dup 改掉", nil)
	if !strings.Contains(res.Answer, "refused") || !strings.Contains(res.Answer, "appears 2 times") {
		t.Fatalf("multiple matches was not a refusal observation: %q", res.Answer)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "dup\ndup\n" {
		t.Fatalf("the file was modified even though the edit was refused: %q", data)
	}
}

// bash 超时是 exit_code=124 的 observation，不是 Go error（design §5.5/§5.8）。
func TestBashTimeoutIsAnObservation(t *testing.T) {
	jobs, err := newJobRegistry(context.Background(), -1) // 关闭自动转后台，前台等到超时
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	start := time.Now()
	res, err := runBash(context.Background(), dir, nil, 100*time.Millisecond, jobs,
		BashArgs{Command: "sleep 5"})
	if err != nil {
		t.Fatalf("a timeout must not be a Go error: %v", err)
	}
	if res.ExitCode != 124 {
		t.Fatalf("exit code = %d, want 124: %+v", res.ExitCode, res)
	}
	if !strings.Contains(res.Output, "timed out") {
		t.Fatalf("the observation does not explain the timeout: %q", res.Output)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the command timeout was not enforced: %s", elapsed)
	}
}

// 前台窗口过后 bash 返回 job_id；作业完成后 job_output 的 wait 能拿到结果
// （design §5.6 的 v1：5 秒转后台 + 结果可读）。
func TestBashAutoBackgroundsAndJobOutput(t *testing.T) {
	jobs, err := newJobRegistry(context.Background(), 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	view := &Jobs{registry: jobs}
	dir := t.TempDir()
	res, err := runBash(context.Background(), dir, nil, 10*time.Second, jobs,
		BashArgs{Command: "sleep 0.4; echo done"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "started background job") {
		t.Fatalf("a slow command did not background: %+v", res)
	}
	list := view.List(context.Background())
	if len(list) != 1 {
		t.Fatalf("jobs list = %+v, want one job", list)
	}
	job, err := view.Wait(context.Background(), list[0].ID)
	if err != nil {
		t.Fatalf("waiting for the job: %v", err)
	}
	if job.Status != "completed" {
		t.Fatalf("job status = %q, want completed", job.Status)
	}
	if !strings.Contains(job.Output, "done") {
		t.Fatalf("job output = %q, want the command's output", job.Output)
	}
	if job.ExitCode != 0 {
		t.Fatalf("job exit code = %d", job.ExitCode)
	}
}

// job_kill 必须真的能停掉一个长命令（本地作业的生命周期由 eino backgroundtask 管）。
func TestJobKillStopsTheCommand(t *testing.T) {
	jobs, err := newJobRegistry(context.Background(), 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	view := &Jobs{registry: jobs}
	dir := t.TempDir()
	res, err := runBash(context.Background(), dir, nil, 30*time.Second, jobs,
		BashArgs{Command: "sleep 30"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "started background job") {
		t.Fatalf("a long command did not background: %+v", res)
	}
	list := view.List(context.Background())
	if len(list) != 1 {
		t.Fatalf("jobs list = %+v", list)
	}
	if err := view.Cancel(context.Background(), list[0].ID, "test cleanup"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	job, err := view.Wait(context.Background(), list[0].ID)
	if err != nil {
		t.Fatalf("wait after cancel: %v", err)
	}
	if job.Status != "canceled" {
		t.Fatalf("job status = %q, want canceled", job.Status)
	}
}

// 工具面必须同时包含自定义 bash、官方文件工具与作业控制工具，且顺序稳定
// （工具列表是提示词缓存前缀的一部分）。
func TestWorkerToolNames(t *testing.T) {
	srv := startMock(t)
	r := newTestRunner(t, testConfig(srv, t.TempDir(), "mock-text"))
	runSessionForTest(t, r, "hi", nil)
	last := srv.LastRequest()
	want := []string{"bash", "read", "write", "edit", "glob", "grep", "job_output", "job_kill"}
	got := map[string]bool{}
	for _, name := range last.ToolNames {
		got[name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Fatalf("worker tool list is missing %q: %v", name, last.ToolNames)
		}
	}
	// 再跑一轮，顺序必须与第一轮一致（缓存前缀稳定）。
	first := strings.Join(last.ToolNames, ",")
	runSessionForTest(t, r, "hi again", nil)
	second := strings.Join(srv.LastRequest().ToolNames, ",")
	if first != second {
		t.Fatalf("tool order changed between turns:\n%s\n%s", first, second)
	}
}

// grep 通过官方工具走完整链路：output_mode=content 返回命中行；没有命中时是
// 模型可读的 "No matches found"，不是整轮失败（旧实现依赖外部 grep，见 F5）。
func TestGrepThroughTool(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "a.go"),
		[]byte("package pkg\n\nfunc needle() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := startMock(t)
	srv.ToolName = "grep"
	srv.ToolArgs = `{"pattern":"needle","output_mode":"content"}`
	r := newTestRunner(t, testConfig(srv, dir, "mock-tool"))
	res := runSessionForTest(t, r, "找 needle", nil)
	if !strings.Contains(res.Answer, "needle") || !strings.Contains(res.Answer, "a.go") {
		t.Fatalf("grep did not report the match: %q", res.Answer)
	}

	// 没有命中的情况：官方工具返回文本，不是 Go error。
	// 先在会话日志里写入同样的字符串——grep 必须跳过 .zeokeel/，否则它会匹配到
	// 自己的 tool_call 参数（自引用），"不存在"的查询会变成命中。
	seed := filepath.Join(dir, ".zeokeel", "sessions", "worker-seed.evlog")
	if err := os.MkdirAll(filepath.Dir(seed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seed, []byte("does-not-exist-anywhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv2 := startMock(t)
	srv2.ToolName = "grep"
	srv2.ToolArgs = `{"pattern":"does-not-exist-anywhere","output_mode":"content"}`
	r2 := newTestRunner(t, testConfig(srv2, dir, "mock-tool"))
	res2 := runSessionForTest(t, r2, "找不存在的东西", nil)
	if !strings.Contains(res2.Answer, "No matches found") {
		t.Fatalf("no-match grep did not return an observation: %q", res2.Answer)
	}
}

// glob 工具必须兑现 ** 跨目录匹配（F2）：模型看到的是真实的三层文件。
func TestGlobThroughTool(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a/b/c/z.go", "a/x.go", "readme.md"} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv := startMock(t)
	srv.ToolName = "glob"
	srv.ToolArgs = `{"pattern":"**/*.go"}`
	r := newTestRunner(t, testConfig(srv, dir, "mock-tool"))
	res := runSessionForTest(t, r, "找所有 go 文件", nil)
	if !strings.Contains(res.Answer, "a/b/c/z.go") {
		t.Fatalf("glob ** did not reach three levels deep: %q", res.Answer)
	}
	if strings.Contains(res.Answer, "readme.md") {
		t.Fatalf("**/*.go matched a markdown file: %q", res.Answer)
	}
}

// 作业必须能活过发起它的回合：回合结束后 runtime 仍能通过 Runner.Jobs() 等到结果，
// 这是 Wave 2 "job 结束唤醒回合"的前提（jobs.go 的 v1 契约）。
func TestBackgroundJobSurvivesTheTurn(t *testing.T) {
	srv := startMock(t)
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"sleep 0.5; echo job-finished"}`

	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-tool", func(c *Config) {
		c.BackgroundAfter = 50 * time.Millisecond
		c.BashTimeout = 5 * time.Second
	}))
	res := runSessionForTest(t, r, "跑一个慢命令", nil)
	if res.Answer == "" {
		t.Fatal("empty answer")
	}

	// 回合已经结束；作业应仍在 registry 里并能被等到完成。
	jobs := r.Jobs().List(context.Background())
	if len(jobs) != 1 {
		t.Fatalf("jobs after the turn = %+v, want the backgrounded command", jobs)
	}
	job, err := r.Jobs().Wait(context.Background(), jobs[0].ID)
	if err != nil {
		t.Fatalf("waiting for the job after the turn: %v", err)
	}
	if job.Status != "completed" || !strings.Contains(job.Output, "job-finished") {
		t.Fatalf("job after the turn = %+v", job)
	}
}

// 遍历类工具必须跳过 agent 自己的元数据目录（.zeokeel/ 会话日志与落盘输出、
// .tmp/ 命令临时文件）：否则 grep 会匹配到自己的 tool_call 参数（自引用），
// glob 会把会话文件当成工作区文件。read 仍可显式读取这些路径。
func TestTraversalSkipsAgentMetadata(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".zeokeel/sessions/worker-1.evlog", "SESSION_ONLY_TOKEN")
	write(".zeokeel/tool-output/call_1.txt", "OFFLOAD_ONLY_TOKEN")
	write(".tmp/scratch.txt", "TMP_ONLY_TOKEN")
	write(".zeokeel/tool-output/off.go", "package off\n")
	write("a/b/c/z.go", "package x\n")

	backend := &localBackend{root: dir}
	ctx := context.Background()
	for _, pattern := range []string{"SESSION_ONLY_TOKEN", "OFFLOAD_ONLY_TOKEN", "TMP_ONLY_TOKEN"} {
		matches, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: pattern})
		if err != nil {
			t.Fatalf("grep %s: %v", pattern, err)
		}
		if len(matches) != 0 {
			t.Fatalf("grep matched its own metadata for %q: %+v", pattern, matches)
		}
	}
	// 工作区里的真实内容仍然能被 grep 找到。
	write("real.txt", "REAL_TOKEN\n")
	matches, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: "REAL_TOKEN"})
	if err != nil || len(matches) != 1 || matches[0].Path != "real.txt" {
		t.Fatalf("grep lost normal matches: %+v err=%v", matches, err)
	}
	// glob 不列出元数据目录里的文件。
	infos, err := backend.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, fi := range infos {
		paths[fi.Path] = true
	}
	if paths[".zeokeel/tool-output/off.go"] {
		t.Fatalf("glob listed agent metadata: %v", paths)
	}
	if !paths["a/b/c/z.go"] {
		t.Fatalf("glob lost a normal file: %v", paths)
	}
	// read 仍然能显式读取落盘的工具输出（reduction 的提示依赖它）。
	fc, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: ".zeokeel/tool-output/call_1.txt"})
	if err != nil || fc.Content != "OFFLOAD_ONLY_TOKEN" {
		t.Fatalf("read of an offloaded tool result must keep working: %+v err=%v", fc, err)
	}
}

// A Worker's shell must not inherit the app's credentials (design §5.5: 执行环境
// 不含凭据；audit 指出 bash 曾继承全量 *KEY*/*TOKEN*）。It must still inherit the
// variables a command needs, run commands, and never write the environment to the
// session log.
func TestBashEnvDropsCredentialsButKeepsAgentVars(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "sk-should-not-leak")
	t.Setenv("GH_TOKEN", "ghp-should-not-leak")
	t.Setenv("DB_PASSWORD", "pw-should-not-leak")
	t.Setenv("MY_CREDENTIALS", "cred-should-not-leak")
	t.Setenv("ZEOKEEL_TEST_SAFE", "kept")

	res, err := runBash(context.Background(), dir, nil, 10*time.Second, nil, BashArgs{Command: "env"})
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{
		"sk-should-not-leak", "OPENAI_API_KEY",
		"ghp-should-not-leak", "GH_TOKEN",
		"pw-should-not-leak", "DB_PASSWORD",
		"cred-should-not-leak", "MY_CREDENTIALS",
	} {
		if strings.Contains(res.Output, leak) {
			t.Fatalf("the shell inherited or printed a credential (%q):\n%s", leak, res.Output)
		}
	}
	for _, keep := range []string{"PATH=", "ZEOKEEL_WORKDIR=", "ZEOKEEL_TEST_SAFE=kept"} {
		if !strings.Contains(res.Output, keep) {
			t.Fatalf("the shell lost a variable a command needs (%q)", keep)
		}
	}
	// The model can still run an ordinary command through the same path.
	out, err := runBash(context.Background(), dir, nil, 10*time.Second, nil, BashArgs{Command: "echo worker-still-runs"})
	if err != nil || out.ExitCode != 0 || !strings.Contains(out.Output, "worker-still-runs") {
		t.Fatalf("a normal command stopped working: %+v err=%v", out, err)
	}
	// Unit-level: case-insensitive matching, whitelist passthrough.
	got := strings.Join(scrubEnv([]string{"PATH=/bin", "HOME=/root", "LANG=C.UTF-8", "api_Key=v", "someToken=x", "HOME_SECRET=y"}), ",")
	if !strings.Contains(got, "PATH=/bin") || !strings.Contains(got, "HOME=/root") || !strings.Contains(got, "LANG=C.UTF-8") {
		t.Fatalf("scrubEnv dropped a needed variable: %s", got)
	}
	if strings.Contains(got, "api_Key") || strings.Contains(got, "someToken") || strings.Contains(got, "HOME_SECRET") {
		t.Fatalf("scrubEnv kept a credential-looking variable: %s", got)
	}
}

// The same guarantee on the production ADK path, including the session log: the
// model's tool output (scrubbed env) must not contain the secret, and nothing may
// log the original environment.
func TestModelBashCannotSeeEnvSecrets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-model-path-secret")
	srv := startMock(t)
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"env"}`
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-tool"))

	res := runSessionForTest(t, r, "看看环境变量", nil)
	if strings.Contains(res.Answer, "sk-model-path-secret") || strings.Contains(res.Answer, "OPENAI_API_KEY") {
		t.Fatalf("the model saw a credential through bash: %q", res.Answer)
	}
	if !strings.Contains(res.Answer, "PATH=") {
		t.Fatalf("the model's command lost PATH: %q", res.Answer)
	}
	// The session event log (the durable artifact the panel reads) must not carry
	// the credential either.
	logs, err := filepath.Glob(filepath.Join(r.Config().SessionDir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("no session log written")
	}
	for _, path := range logs {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "sk-model-path-secret") || strings.Contains(string(data), "OPENAI_API_KEY") {
			t.Fatalf("the session log contains a credential: %s", path)
		}
	}
}

// Config.Env carries the runtime's non-secret per-Ticket ZEOKEEL_* variables into
// the bash child after the OS environment is scrubbed. Config.Env wins over
// same-named OS/agent defaults, and secrets still never appear.
func TestBashEnvInjectsConfigEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-config-env-secret")
	dir := t.TempDir()
	extra := map[string]string{
		"ZEOKEEL_TICKET":    "#42",
		"ZEOKEEL_WORKSPACE": "webapp",
		// Duplicates of the defaults the shell adds itself: Config.Env must win.
		"ZEOKEEL_WORKDIR": "custom-root",
		"TMPDIR":          "custom-tmp",
	}
	res, err := runBashEnv(context.Background(), dir, nil, 10*time.Second, nil, extra, BashArgs{Command: "env"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ZEOKEEL_TICKET=#42",
		"ZEOKEEL_WORKSPACE=webapp",
		"ZEOKEEL_WORKDIR=custom-root",
		"TMPDIR=custom-tmp",
		"PATH=",
	} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("the bash child is missing Config.Env/default %q", want)
		}
	}
	if strings.Contains(res.Output, "sk-config-env-secret") || strings.Contains(res.Output, "OPENAI_API_KEY") {
		t.Fatalf("Config.Env injection re-exposed an OS credential: %s", res.Output)
	}
	// A normal command still runs with the injected environment.
	out, err := runBashEnv(context.Background(), dir, nil, 10*time.Second, nil, extra,
		BashArgs{Command: "echo injected-ok"})
	if err != nil || out.ExitCode != 0 || !strings.Contains(out.Output, "injected-ok") {
		t.Fatalf("normal command with Config.Env failed: %+v err=%v", out, err)
	}
}

// The production ADK path sees Config.Env too, and the session log never carries
// the OS secret that was scrubbed.
func TestModelSeesOnlyConfigEnv(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp-model-env-secret")
	srv := startMock(t)
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"env"}`
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-tool", func(c *Config) {
		c.Env = map[string]string{"ZEOKEEL_TICKET": "#42", "ZEOKEEL_WORKSPACE": "webapp"}
	}))
	res := runSessionForTest(t, r, "看看环境变量", nil)
	for _, want := range []string{"ZEOKEEL_TICKET=#42", "ZEOKEEL_WORKSPACE=webapp", "PATH="} {
		if !strings.Contains(res.Answer, want) {
			t.Fatalf("the model's shell is missing Config.Env %q: %q", want, res.Answer)
		}
	}
	if strings.Contains(res.Answer, "ghp-model-env-secret") || strings.Contains(res.Answer, "GH_TOKEN") {
		t.Fatalf("the model saw a scrubbed OS credential: %q", res.Answer)
	}
	logs, err := filepath.Glob(filepath.Join(r.Config().SessionDir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range logs {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "ghp-model-env-secret") {
			t.Fatalf("a session log leaked the scrubbed credential: %s", path)
		}
	}
}
