package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk/backgroundtask"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// workspaceTools builds the tools a Worker gets that are not part of the official
// filesystem middleware: the shell. File read/write/edit/glob/grep are registered
// by eino's adk/middlewares/filesystem over a localBackend (workspace.go).
//
// Every path is resolved inside cfg.Workdir, which is the directory the Ticket owns.
func workspaceTools(cfg Config) ([]tool.BaseTool, *jobRegistry, error) {
	root, err := filepath.Abs(cfg.Workdir)
	if err != nil {
		return nil, nil, fmt.Errorf("agent: workdir: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, nil, fmt.Errorf("agent: create workdir: %w", err)
	}
	blocked := newBlockedCommands(cfg.BlockedCommands)
	jobs, err := newJobRegistry(context.Background(), cfg.BackgroundAfter)
	if err != nil {
		return nil, nil, err
	}

	bash, err := utils.InferTool("bash",
		"Run a shell command in the workspace and return its output. The working directory is the workspace root. "+
			"A non-zero exit code is returned as a result (not a failure), so you can read it and decide what to do. "+
			"Long output is truncated with a pointer to the full text on disk. "+
			"A command still running after the foreground window moves to a background job and the result names its job_id.",
		func(ctx context.Context, in BashArgs) (ToolResult, error) {
			return runBashEnv(ctx, root, blocked, cfg.BashTimeout, jobs, cfg.Env, in)
		})
	if err != nil {
		return nil, nil, err
	}
	readImage, err := newReadImageTool(root)
	if err != nil {
		return nil, nil, err
	}
	return []tool.BaseTool{bash, readImage}, jobs, nil
}

// BashArgs is one shell command.
type BashArgs struct {
	Command string `json:"command" jsonschema:"description=the shell command to run"`
	Timeout int    `json:"timeout_seconds,omitempty" jsonschema:"description=seconds before the command is killed, default 60"`
}

// ToolResult is the observation the bash tool returns to the model. File tools now
// return the official filesystem middleware's plain text, so the old diff fields
// (which nothing set after the migration) are gone.
type ToolResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code,omitempty"`
}

// runBash runs one command in the workspace, moving it to a background job when it
// outlives the foreground window (design §5.6: 前台 5 秒转后台).
//
// Exit codes are observations, never Go errors: a slow command or a failing one is
// something the model must read and decide about, not a reason to fail the whole
// Ticket (design §5.5/§5.8; eino treats a tool error as a failed turn).
// runBash is the env-free entry point kept for tests/other callers; the Worker
// tool calls runBashEnv so Config.Env reaches the child (see Config.Env).
func runBash(ctx context.Context, root string, blocked blockedCommands, def time.Duration,
	jobs *jobRegistry, in BashArgs) (ToolResult, error) {
	return runBashEnv(ctx, root, blocked, def, jobs, nil, in)
}

func runBashEnv(ctx context.Context, root string, blocked blockedCommands, def time.Duration,
	jobs *jobRegistry, extraEnv map[string]string, in BashArgs) (ToolResult, error) {
	cmdline := strings.TrimSpace(in.Command)
	if cmdline == "" {
		return refusal("command is required"), nil
	}
	// The blocked list is advisory: a program can still be run by absolute path.
	// Design §5.5 says so plainly; isolation needs an OS account, not this check.
	if entry, ok := blocked.match(cmdline); ok {
		return ToolResult{Output: "refused: " + entry + " is blocked for this workspace", ExitCode: 126}, nil
	}
	timeout := time.Duration(in.Timeout) * time.Second
	if timeout <= 0 {
		timeout = def
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if jobs == nil {
		return executeShell(ctx, root, cmdline, timeout, extraEnv), nil
	}
	work := func(workCtx context.Context, _ backgroundtask.ExecutionRuntime) (string, error) {
		// The run context stays alive while the caller keeps the Runner, so a job
		// can outlive the turn; job_kill and cancellation of the run reach it
		// through this ctx. The command timeout below is its own bound.
		cmdCtx, cancel := context.WithTimeout(workCtx, timeout)
		defer cancel()
		res := executeShell(cmdCtx, root, cmdline, timeout, extraEnv)
		encoded, err := json.Marshal(res)
		if err != nil {
			return res.Output, nil
		}
		return string(encoded), nil
	}
	task, err := jobs.run(ctx, cmdline, work)
	if err != nil {
		var foregroundTimeout *backgroundtask.ForegroundTimeoutError
		if errors.As(err, &foregroundTimeout) {
			return ToolResult{
				Output:   fmt.Sprintf("timed out after %s (exit code 124); the command was killed", foregroundTimeout.Timeout),
				ExitCode: 124,
			}, nil
		}
		return ToolResult{}, err
	}
	if task == nil {
		return ToolResult{Output: "refused: the job manager returned no task"}, nil
	}
	switch task.Status {
	case backgroundtask.StatusCompleted:
		var res ToolResult
		if err := json.Unmarshal(task.ResultData, &res); err != nil {
			return ToolResult{Output: string(task.ResultData)}, nil
		}
		return res, nil
	case backgroundtask.StatusFailed:
		return ToolResult{Output: "command failed to start: " + task.ResultError, ExitCode: 1}, nil
	default:
		return ToolResult{Output: fmt.Sprintf(
			"started background job %s (%s). It is still running; use job_output with task_id=%q to read its output, "+
				"or job_kill with task_id=%q to stop it.", task.Spec.ID, cmdline, task.Spec.ID, task.Spec.ID)}, nil
	}
}

// executeShell runs one command with a hard timeout and returns an observation.
// A timeout is exit code 124, the shell convention (design §5.5).
func executeShell(ctx context.Context, root, cmdline string, timeout time.Duration,
	extraEnv map[string]string) ToolResult {
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
	// sh -c may leave grandchildren holding the output pipe after the shell is
	// killed (a server started by the command is the useful case). Without
	// WaitDelay, CombinedOutput would block until those grandchildren exit, which
	// turns "command timed out" into "turn hung for minutes".
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Dir = root
	// Keep temp files next to the work rather than in the shared /tmp, where one
	// ticket's scratch files would be visible to the next one. This is hygiene, not
	// isolation: design §5.5 is explicit that only an OS account or a container
	// gives a real boundary.
	cmd.Env = buildShellEnv(root, extraEnv)
	out, err := cmd.CombinedOutput()
	res := ToolResult{Output: truncate(string(out), 8000)}
	if ctx.Err() == context.DeadlineExceeded {
		res.ExitCode = 124
		res.Output = strings.TrimRight(res.Output, "\n")
		if res.Output != "" {
			res.Output += "\n"
		}
		res.Output += fmt.Sprintf("timed out after %s (exit code 124); the command was killed", timeout)
		return res
	}
	// The shell exited but a child it started kept the pipe open. The command
	// itself succeeded; the child is exactly the kind of process that should become
	// a background job.
	if errors.Is(err, exec.ErrWaitDelay) {
		return res
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		res.ExitCode = exit.ExitCode()
		return res
	}
	if err != nil {
		if res.Output != "" && !strings.HasSuffix(res.Output, "\n") {
			res.Output += "\n"
		}
		res.Output += "cannot run command: " + err.Error()
		res.ExitCode = 126
		return res
	}
	return res
}

// sensitiveEnvWords drop a variable from the shell child's environment
// case-insensitively. The list mirrors design §5.5 / 006's "不含凭据" rule and the
// audit finding that a Worker's bash used to inherit *KEY*/*TOKEN* from the app.
var sensitiveEnvWords = []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "CREDENTIAL"}

// scrubEnv removes credential-looking variables from an environment without
// otherwise changing it: the Worker keeps PATH/HOME/TMPDIR/LANG/ZEOKEEL_* and any
// deployment-provided non-secret variables it needs.
//
// Nothing here logs the dropped names/values; the environment of the app is never
// written to the session log or the panel.
func scrubEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if isSensitiveEnvName(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func isSensitiveEnvName(name string) bool {
	upper := strings.ToUpper(name)
	for _, word := range sensitiveEnvWords {
		if strings.Contains(upper, word) {
			return true
		}
	}
	return false
}

// buildShellEnv is the child environment of one bash command, in order:
//
//	scrubbed OS environment -> Config.Env -> TMPDIR/ZEOKEEL_WORKDIR defaults.
//
// Config.Env wins over same-named variables (the runtime owns per-Ticket
// ZEOKEEL_* values). TMPDIR/ZEOKEEL_WORKDIR are still added when Config.Env does
// not set them, so per-Ticket temp files stay inside the workspace.
func buildShellEnv(root string, extraEnv map[string]string) []string {
	env := mergeEnv(scrubEnv(os.Environ()), extraEnv)
	tmp := filepath.Join(root, ".tmp")
	if os.MkdirAll(tmp, 0o755) == nil {
		if _, set := extraEnv["TMPDIR"]; !set {
			env = mergeEnv(env, map[string]string{"TMPDIR": tmp})
		}
	}
	if _, set := extraEnv["ZEOKEEL_WORKDIR"]; !set {
		env = mergeEnv(env, map[string]string{"ZEOKEEL_WORKDIR": root})
	}
	return env
}

// mergeEnv overlays overrides on an environment slice, dropping duplicate names
// (override wins). Keys are applied in sorted order so the child environment is
// deterministic and testable.
func mergeEnv(env []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return env
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(env)+len(keys))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, replaced := overrides[name]; replaced {
			continue
		}
		out = append(out, kv)
	}
	for _, k := range keys {
		out = append(out, k+"="+overrides[k])
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n… (%d bytes truncated)", len(s)-n)
}

// refusal turns a tool-level problem into an observation the model can act on.
//
// A tool error that aborts the whole run is the wrong shape: a model that asks for
// a path outside the workspace should be told "no", then try again inside it. Only
// infrastructure failures (provider down, disk gone) should end the run
// (design §5.5 + §5.8).
func refusal(msg string) ToolResult {
	return ToolResult{Output: "refused: " + msg, ExitCode: 126}
}

// blockedCommands 是"同名命令不可用"的咨询性名单（design §5.5 明确说它不是安全
// 边界）。匹配按整条命令的前缀做：配置里的 "git push --force" 必须能拦住
// "git push --force origin main"，而旧的 firstWord 查表永远匹配不到多词条目。
type blockedCommands []string

func newBlockedCommands(list []string) blockedCommands {
	out := make(blockedCommands, 0, len(list))
	for _, entry := range list {
		if norm := normalizeCommand(entry); norm != "" {
			out = append(out, norm)
		}
	}
	return out
}

// match 返回命中的最长前缀；前缀越长越具体（"git push --force" 优先于 "git"）。
func (b blockedCommands) match(cmdline string) (string, bool) {
	norm := normalizeCommand(cmdline)
	best := ""
	for _, entry := range b {
		if norm == entry || strings.HasPrefix(norm, entry+" ") {
			if len(entry) > len(best) {
				best = entry
			}
		}
	}
	return best, best != ""
}

// normalizeCommand collapses runs of whitespace so a command matches regardless of
// how the model spaced it. Newlines/tabs become single spaces, which is what
// `sh -c` would treat as argument separators anyway.
func normalizeCommand(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
