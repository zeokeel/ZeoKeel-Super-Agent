package agent

// eino's built-in agent scaffolding that the Worker adopts instead of hand-rolling
// it: plantask (a structured plan/todo list persisted as JSON next to the work) and
// agentsmd (the repository's AGENTS.md injected into the model input). Both are
// opt-in on Config so tests/demos and deployments that do not want them stay clean.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/agentsmd"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
)

const (
	// agentsMDFile is the file the middleware injects, relative to the workspace
	// root. It is the repository's own instruction file, not a product prompt.
	agentsMDFile = "AGENTS.md"
	// agentsMDMaxBytes bounds the injected content so a huge instruction file cannot
	// eat the context budget.
	agentsMDMaxBytes = 256 * 1024
)

// planTaskToolNames are the tools eino's plantask middleware injects. They are
// PascalCase in the framework; the helper keeps that one fact in one place for the
// whitelist code and tests.
var planTaskToolNames = []string{
	plantask.TaskCreateToolName,
	plantask.TaskGetToolName,
	plantask.TaskUpdateToolName,
	plantask.TaskListToolName,
}

// newPlanTaskMiddleware wires eino's plantask tools over the workspace root. Tasks
// are plain JSON files in BaseDir, so they survive a restart with the workspace and
// the model can read them with its normal file tools.
func newPlanTaskMiddleware(ctx context.Context, workdir string) (adk.ChatModelAgentMiddleware, error) {
	return plantask.New(ctx, &plantask.Config{
		Backend: &planTaskBackend{local: &localBackend{root: workdir}},
		BaseDir: workdir,
	})
}

// planTaskBackend adapts the workspace os.Root backend to plantask.Backend, which
// additionally needs Delete. LsInfo/Read/Write share the aliased filesystem types.
type planTaskBackend struct {
	local *localBackend
}

var _ plantask.Backend = (*planTaskBackend)(nil)

func (b *planTaskBackend) LsInfo(ctx context.Context, req *plantask.LsInfoRequest) ([]plantask.FileInfo, error) {
	return b.local.LsInfo(ctx, req)
}

func (b *planTaskBackend) Read(ctx context.Context, req *plantask.ReadRequest) (*filesystem.FileContent, error) {
	return b.local.Read(ctx, req)
}

func (b *planTaskBackend) Write(ctx context.Context, req *plantask.WriteRequest) error {
	return b.local.Write(ctx, req)
}

// Delete removes a task file. A file that is already gone is not an error: task
// updates may try to delete a task twice.
func (b *planTaskBackend) Delete(_ context.Context, req *plantask.DeleteRequest) error {
	if req == nil || strings.TrimSpace(req.FilePath) == "" {
		return errors.New("plantask: delete needs a file path")
	}
	rel, err := b.local.rootPath(req.FilePath)
	if err != nil {
		return err
	}
	return b.local.withRoot(func(root *os.Root) error {
		err := root.Remove(rel)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	})
}

// newAgentsMDMiddleware builds the agentsmd middleware for one workspace.
//
// The Backend returns raw os.Root errors (not the Worker's refusal observations)
// because the loader distinguishes "missing file" (a warning) from real I/O
// errors. AGENTS.md is optional, so the warning callback stays quiet instead of
// logging on every turn of every workspace that has none.
func newAgentsMDMiddleware(ctx context.Context, workdir string) (adk.ChatModelAgentMiddleware, error) {
	return agentsmd.New(ctx, &agentsmd.Config{
		Backend:             agentsMDBackend{root: workdir},
		AgentsMDFiles:       []string{agentsMDFile},
		AllAgentsMDMaxBytes: agentsMDMaxBytes,
		OnLoadWarning:       func(string, error) {},
	})
}

// agentsMDBackend reads AGENTS.md through the same os.Root confinement as the file
// tools, but preserves the original error so agentsmd can skip a missing file.
type agentsMDBackend struct {
	root string
}

var _ agentsmd.Backend = agentsMDBackend{}

func (b agentsMDBackend) Read(_ context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	if req == nil || strings.TrimSpace(req.FilePath) == "" {
		return nil, fmt.Errorf("agentsmd: path is required")
	}
	local := &localBackend{root: b.root}
	rel, err := local.rootPath(req.FilePath)
	if err != nil {
		return nil, err
	}
	var data []byte
	err = local.withRoot(func(root *os.Root) error {
		var readErr error
		data, readErr = root.ReadFile(rel)
		return readErr
	})
	if err != nil {
		return nil, err // raw *fs.PathError: agentsmd needs errors.Is(err, os.ErrNotExist)
	}
	return &filesystem.FileContent{Content: string(data)}, nil
}

// isObservationOnlyTool reports whether a tool's errors are user-level problems
// that should come back to the model as an observation instead of failing the turn.
func isObservationOnlyTool(name string) bool {
	for _, taskTool := range planTaskToolNames {
		if name == taskTool {
			return true
		}
	}
	return false
}
