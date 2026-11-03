package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/subagent"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

// newSubagents turns the configured subagents into the official subagent
// middleware. Each subagent is a real adk agent with its own instruction and a
// filtered tool set, so "who may do what" is expressed in what it can call rather
// than in what the prompt asks it not to do (design §5.6).
func (r *Runner) newSubagents(ctx context.Context, mb model.ToolCallingChatModel) ([]adk.ChatModelAgentMiddleware, error) {
	if len(r.cfg.Subagents) == 0 {
		return nil, nil
	}
	agents := make([]adk.Agent, 0, len(r.cfg.Subagents))
	for _, spec := range r.cfg.Subagents {
		if err := validateSubagent(spec); err != nil {
			return nil, err
		}
		tools, handlers, err := r.toolsFor(spec.Tools)
		if err != nil {
			return nil, err
		}
		ag, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
			Name:        spec.Name,
			Description: spec.Description,
			Instruction: spec.Instruction,
			// The parent's recorder is not wired here on purpose: a subagent's
			// text is internal reasoning, and showing it in the timeline would
			// bury the deliverable the person actually reads.
			Model: mb,
			ToolsConfig: adk.ToolsConfig{
				ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
			},
			MaxIterations: maxOr(spec.MaxIterations, 8),
			// File tools come from the official filesystem middleware, so the
			// whitelist has to add that middleware, not just a tool slice.
			Handlers: append(handlers, newToolRefusalMiddleware()),
		})
		if err != nil {
			return nil, fmt.Errorf("agent: subagent %q: %w", spec.Name, err)
		}
		agents = append(agents, ag)
	}
	mw, err := subagent.New(ctx, &subagent.Config{SubAgents: agents})
	if err != nil {
		return nil, fmt.Errorf("agent: subagent middleware: %w", err)
	}
	return []adk.ChatModelAgentMiddleware{mw}, nil
}

// toolsFor selects the parent's tools this subagent may use. An empty list means
// every tool, which is the parent's own set.
//
// File tools (read/write/edit/glob/grep) are registered by the official filesystem
// middleware rather than by a flat tool slice, so the selection also returns the
// middleware instances the subagent has to be constructed with.
func (r *Runner) toolsFor(names []string) ([]tool.BaseTool, []adk.ChatModelAgentMiddleware, error) {
	wantAll := len(names) == 0
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []tool.BaseTool
	for _, t := range r.tools {
		info, err := t.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		if wantAll || want[info.Name] {
			out = append(out, t)
			delete(want, info.Name)
		}
	}
	fileEnabled := map[string]bool{}
	for _, n := range fileToolNames {
		if wantAll || want[n] {
			fileEnabled[n] = true
			delete(want, n)
		}
	}
	var handlers []adk.ChatModelAgentMiddleware
	if len(fileEnabled) > 0 {
		mw, err := newFilesystemMiddleware(context.Background(), r.cfg.Workdir, fileEnabled)
		if err != nil {
			return nil, nil, fmt.Errorf("agent: subagent filesystem middleware: %w", err)
		}
		handlers = append(handlers, mw)
	}
	// The plan/task tools come from eino's plantask middleware as well, so a
	// whitelist that names them has to construct that middleware. The middleware
	// registers the whole task tool set (the framework has no per-tool switch).
	planWanted, planExplicit := false, false
	for _, name := range planTaskToolNames {
		if want[name] {
			planExplicit = true
			planWanted = true
			delete(want, name)
		} else if wantAll {
			// "no list" means the parent's tools; a disabled middleware is simply
			// not part of them, unlike an explicit request (see below).
			planWanted = true
		}
	}
	if planWanted && !r.cfg.PlanTasks {
		if !planExplicit {
			planWanted = false
		} else {
			return nil, nil, fmt.Errorf("agent: subagent asks for plan task tools, but PlanTasks is disabled")
		}
	}
	if planWanted {
		mw, err := newPlanTaskMiddleware(context.Background(), r.cfg.Workdir)
		if err != nil {
			return nil, nil, fmt.Errorf("agent: subagent plantask middleware: %w", err)
		}
		handlers = append(handlers, mw)
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for n := range want {
			missing = append(missing, n)
		}
		sort.Strings(missing)
		return nil, nil, fmt.Errorf("agent: subagent asks for unknown tools: %s", strings.Join(missing, ", "))
	}
	if len(out) == 0 && len(fileEnabled) == 0 && !planWanted {
		return nil, nil, fmt.Errorf("agent: a subagent must keep at least one tool")
	}
	return out, handlers, nil
}

// validateSubagent refuses a spec that would produce a useless or confusing agent.
func validateSubagent(s SubagentSpec) error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("agent: subagent without a name")
	}
	if strings.TrimSpace(s.Description) == "" {
		return fmt.Errorf("agent: subagent %q has no description: the main agent picks by it", s.Name)
	}
	if strings.TrimSpace(s.Instruction) == "" {
		return fmt.Errorf("agent: subagent %q has no instruction", s.Name)
	}
	return nil
}

func maxOr(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}
