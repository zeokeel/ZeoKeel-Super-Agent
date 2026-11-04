package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/backgroundtask"
	"github.com/cloudwego/eino/adk/backgroundtask/local"
	adkbackgroundtask "github.com/cloudwego/eino/adk/middlewares/backgroundtask"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// defaultBackgroundAfter 是 design §5.6 的"前台 5 秒转后台"：命令在前台等 5 秒，
// 还没结束就交给后台作业，回合可以继续（模型拿到 job_id，之后用 job_output 读结果）。
const defaultBackgroundAfter = 5 * time.Second

// 作业控制工具名。它们由 eino 官方 adk/middlewares/backgroundtask 注入，这里只
// 覆盖名字与说明：官方默认名是 task_output/task_stop，产品的工具面用 jobs 语义。
const (
	jobOutputToolName = "job_output"
	jobKillToolName   = "job_kill"
)

const (
	jobOutputDesc = "Read the output of a background shell job started by bash. " +
		"task_id is the job id printed by bash. Blocks until the job finishes " +
		"(default up to 30s) unless block=false. Returns the job status and, once finished, its result."
	jobKillDesc = "Stop a running background shell job (task_id is the job id printed by bash) and return its final state."
)

const jobInstruction = `耗时超过前台窗口的 bash 命令会自动转入后台作业，bash 的返回里会给出 job_id（工具字段名 task_id）。
用 job_output（task_id=job_id）读取后台作业的输出：默认等到作业结束才返回；block=false 只取当前状态。
用 job_kill（task_id=job_id）停止不再需要的后台作业。作业在回合结束后仍会继续运行，所以不要因为命令慢就重复执行。`

// jobRegistry 是 internal/agent 持有的作业子系统：eino 的 backgroundtask.Manager
// 负责生命周期与工具，local.Runner 负责"前台 5 秒后转后台"的进程内执行。
// 本地作业不落盘（v1）：进程崩溃后作业丢失，这是 design §5.6 明确接受的简化。
type jobRegistry struct {
	mgr   *backgroundtask.Manager
	local *local.Runner

	mu  sync.Mutex
	ids []string
}

func newJobRegistry(ctx context.Context, foreground time.Duration) (*jobRegistry, error) {
	if foreground == 0 {
		foreground = defaultBackgroundAfter
	}
	// eino 的约定：0 表示默认值，负数表示"不自动转后台"（前台等到命令超时）。
	foregroundMs := -1
	if foreground > 0 {
		foregroundMs = int(foreground / time.Millisecond)
		if foregroundMs <= 0 {
			foregroundMs = 1
		}
	}
	executors := backgroundtask.NewExecutorRegistry()
	mgr, err := backgroundtask.New(ctx, &backgroundtask.Config{Executors: executors})
	if err != nil {
		return nil, fmt.Errorf("agent: job manager: %w", err)
	}
	runner, err := local.New(&local.Config{
		Manager:             mgr,
		Executors:           executors,
		ForegroundTimeoutMs: &foregroundMs,
		// The product policy is "any command that outlives the foreground window
		// becomes a job", not "ask the model first". design §5.6.
		ShouldAutoBackground: func(context.Context, *backgroundtask.ForegroundCandidate) bool {
			return true
		},
	})
	if err != nil {
		return nil, fmt.Errorf("agent: job runner: %w", err)
	}
	return &jobRegistry{mgr: mgr, local: runner}, nil
}

// run executes one unit of work through the background-task lifecycle. The
// returned Task is the current snapshot: terminal for a foreground run, pending or
// running for one that was auto-backgrounded.
func (j *jobRegistry) run(ctx context.Context, description string, work local.WorkFunc) (*backgroundtask.Task, error) {
	task, err := j.local.Run(ctx, &local.Input{
		Description: description,
		Kind:        "bash",
		Payload:     []byte(description),
	}, work)
	if err != nil {
		return nil, err
	}
	if task != nil && !jobTerminal(task.Status) {
		j.track(task.Spec.ID)
	}
	return task, nil
}

func (j *jobRegistry) track(id string) {
	if id == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, existing := range j.ids {
		if existing == id {
			return
		}
	}
	j.ids = append(j.ids, id)
}

func (j *jobRegistry) tracked() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.ids...)
}

func jobTerminal(status backgroundtask.Status) bool {
	switch status {
	case backgroundtask.StatusCompleted, backgroundtask.StatusFailed, backgroundtask.StatusCanceled:
		return true
	}
	return false
}

// Job is a snapshot of one background shell job, as exposed to the host (runtime
// and the panel). It is deliberately narrow: runtime must not need the eino
// backgroundtask types to observe jobs.
type Job struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Output      string     `json:"output,omitempty"`
	ExitCode    int        `json:"exit_code,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	DoneAt      *time.Time `json:"done_at,omitempty"`
}

func jobFromTask(task *backgroundtask.Task) Job {
	if task == nil {
		return Job{}
	}
	job := Job{
		ID:          task.Spec.ID,
		Description: task.Spec.Description,
		Status:      string(task.Status),
		CreatedAt:   task.CreatedAt,
		DoneAt:      task.DoneAt,
	}
	if len(task.ResultData) > 0 {
		// The bash job stores its ToolResult as JSON so the exit code survives the
		// trip through the background store; fall back to raw text when it is not.
		var res ToolResult
		if err := json.Unmarshal(task.ResultData, &res); err == nil {
			job.Output = res.Output
			job.ExitCode = res.ExitCode
		} else {
			job.Output = string(task.ResultData)
		}
	}
	if task.ResultError != "" {
		job.Error = task.ResultError
	}
	return job
}

// Jobs is the host-facing view of the Worker's background shell jobs.
type Jobs struct {
	registry *jobRegistry
}

// Jobs returns the Worker's background-job registry. The runtime can use it to
// list jobs after a turn and to wait for one before starting the next turn; see
// the package docs for the Wave-2 wiring.
func (r *Runner) Jobs() *Jobs {
	if r == nil || r.jobs == nil {
		return &Jobs{}
	}
	return &Jobs{registry: r.jobs}
}

// List returns every job this Worker started, oldest first.
func (j *Jobs) List(ctx context.Context) []Job {
	if j == nil || j.registry == nil {
		return nil
	}
	ids := j.registry.tracked()
	out := make([]Job, 0, len(ids))
	for _, id := range ids {
		task, err := j.registry.mgr.Get(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, jobFromTask(task))
	}
	return out
}

// Get returns one job by id.
func (j *Jobs) Get(ctx context.Context, id string) (Job, error) {
	if j == nil || j.registry == nil {
		return Job{}, errors.New("agent: no job registry")
	}
	task, err := j.registry.mgr.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	return jobFromTask(task), nil
}

// Wait blocks until the job reaches a terminal state (or ctx is done). The runtime
// uses it to decide whether a finished turn should be resumed with the job result.
func (j *Jobs) Wait(ctx context.Context, id string) (Job, error) {
	if j == nil || j.registry == nil {
		return Job{}, errors.New("agent: no job registry")
	}
	task, err := j.registry.mgr.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	for !jobTerminal(task.Status) {
		next, waitErr := j.registry.mgr.WaitForTaskVersion(ctx, &backgroundtask.WaitForTaskVersionRequest{
			TaskID: id, AfterVersion: task.Version,
		})
		if waitErr != nil {
			return jobFromTask(task), waitErr
		}
		task = next
	}
	return jobFromTask(task), nil
}

// Cancel asks the job to stop. It is advisory in the same way blocked commands
// are: the running process is stopped through its context, then the task records
// the cancellation.
func (j *Jobs) Cancel(ctx context.Context, id, reason string) error {
	if j == nil || j.registry == nil {
		return errors.New("agent: no job registry")
	}
	_, err := j.registry.mgr.RequestCancel(ctx, id,
		backgroundtask.WithCancellationReason(reason))
	return err
}

// newJobsMiddleware exposes the job control tools (job_output/job_kill) through
// eino's official background-task middleware. The middleware also appends a short
// instruction; it is written here instead of using the framework default so the
// Worker's prompt stays in this project's voice (design §5.6).
func (r *Runner) newJobsMiddleware(ctx context.Context) (adk.ChatModelAgentMiddleware, error) {
	if r.jobs == nil {
		return nil, errors.New("agent: job registry is not initialized")
	}
	outputDesc, killDesc := jobOutputDesc, jobKillDesc
	return adkbackgroundtask.New(ctx, &adkbackgroundtask.Config{
		Manager: r.jobs.mgr,
		TaskOutputToolConfig: &adkbackgroundtask.ToolConfig{
			Name: jobOutputToolName, Desc: &outputDesc,
		},
		TaskStopToolConfig: &adkbackgroundtask.ToolConfig{
			Name: jobKillToolName, Desc: &killDesc,
		},
		CustomSystemPrompt: func(context.Context, *adkbackgroundtask.SystemPromptInput) string {
			return jobInstruction
		},
	})
}

// jobListToolName is DSH's job-control set: job_output/job_list/job_kill. The
// output/kill tools come from eino's backgroundtask middleware; listing is this
// small tool over the same registry.
const (
	jobListToolName = "job_list"
	jobListDesc     = "List the background shell jobs of this session, newest first: id, status, exit code and description. " +
		"Use job_output to read a job's result and job_kill to stop one."
)

func newJobListTool(jobs *jobRegistry) (tool.BaseTool, error) {
	return utils.InferTool(jobListToolName, jobListDesc, func(ctx context.Context, _ struct{}) (string, error) {
		if jobs == nil {
			return "no background jobs", nil
		}
		ids := jobs.tracked()
		if len(ids) == 0 {
			return "no background jobs", nil
		}
		var b strings.Builder
		for i := len(ids) - 1; i >= 0; i-- {
			task, err := jobs.mgr.Get(ctx, ids[i])
			if err != nil {
				continue
			}
			job := jobFromTask(task)
			fmt.Fprintf(&b, "%s  %s  exit=%d  %s\n", job.ID, job.Status, job.ExitCode, job.Description)
		}
		out := strings.TrimRight(b.String(), "\n")
		if out == "" {
			return "no background jobs", nil
		}
		return out, nil
	})
}
