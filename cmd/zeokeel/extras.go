package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/goal"
	"github.com/zeokeel/super-agent/internal/mcp"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/schedule"
	"github.com/zeokeel/super-agent/internal/skills"
	"github.com/zeokeel/super-agent/internal/store"
)

// extras are the optional capabilities a deployment can switch on: a skill
// library, MCP servers, timers and chat drivers. Each one is independent so a
// broken server cannot stop the app from opening (design §5.10).
type extras struct {
	skills  *skills.Set
	mcp     *mcp.Manager
	sched   *schedule.Manager
	goals   *goal.Service
	closeFn func()
}

// loadExtras opens what the flags asked for and reports, not fails, on problems:
// a missing directory is a configuration mistake, not a reason to lose the window.
func loadExtras(st *store.Store, skillsDir, mcpFile string, logger func(string, ...any)) (*extras, error) {
	e := &extras{}
	if skillsDir != "" {
		set, err := skills.Load(skillsDir, skills.Options{})
		if err != nil {
			logger("技能库加载失败（继续运行）: %v", err)
		} else {
			e.skills = set
			logger("已加载 %d 个技能", set.Len())
		}
	}
	if mcpFile != "" {
		cfg, err := readMCPConfig(mcpFile)
		if err != nil {
			logger("MCP 配置读取失败（继续运行）: %v", err)
		} else if mgr, err := mcp.NewManager(cfg); err != nil {
			logger("MCP 初始化失败（继续运行）: %v", err)
		} else if err := mgr.Start(context.Background()); err != nil {
			logger("MCP 启动失败（继续运行）: %v", err)
		} else {
			e.mcp = mgr
			logger("MCP 已启动，%d 个工具", len(mgr.Tools()))
		}
	}
	if st != nil {
		if s, err := schedule.New(st); err != nil {
			logger("定时器初始化失败（继续运行）: %v", err)
		} else {
			e.sched = s
		}
		if g, err := goal.New(st); err != nil {
			logger("目标服务初始化失败（继续运行）: %v", err)
		} else {
			e.goals = g
		}
	}
	e.closeFn = func() {
		if e.mcp != nil {
			_ = e.mcp.Close()
		}
	}
	return e, nil
}

// readMCPConfig accepts the same shape as the design's config file: a list of
// servers, each either stdio (command) or streamable HTTP (url).
func readMCPConfig(path string) (mcp.Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return mcp.Config{}, err
	}
	var cfg mcp.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return mcp.Config{}, err
	}
	return cfg, nil
}

// startScheduleLoop fires due timers as Triggers. The fire id is content-derived
// and doubles as the Trigger id, so a crash between claim and receive produces one
// Trigger, not two (design §7.2).
func startScheduleLoop(ctx context.Context, rt *runtime.Runtime, mgr *schedule.Manager, every time.Duration) {
	if mgr == nil {
		return
	}
	if n, err := mgr.Recover(time.Now()); err == nil && n > 0 {
		// Interrupted fires are recorded as such and released, so the timer keeps
		// its cadence instead of wedging on a dead claim.
		_ = n
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			claims, err := mgr.ClaimDueNow()
			if err != nil {
				continue
			}
			for _, c := range claims {
				if c.Status != "fired" {
					// A skipped round is metadata, not work: it is already recorded.
					_ = mgr.Complete(c.FireID)
					continue
				}
				tr := triggerForSchedule(c)
				if _, err := rt.ReceiveTrigger(tr); err != nil && !errors.Is(err, engine.ErrDuplicate) {
					continue // do not complete: let the next pass retry
				}
				_ = mgr.Complete(c.FireID)
				rt.Publish()
			}
		}
	}()
}

// triggerForSchedule turns a fired timer into the same Trigger shape a chat
// message produces, so nothing downstream needs to know timers exist.
func triggerForSchedule(c schedule.Claim) domain.Trigger {
	tr := domain.Trigger{
		ID:        domain.ID(c.FireID),
		Source:    "schedule",
		Human:     true,
		Text:      scheduledTaskText(c.Definition.ID),
		At:        c.FiredAt,
		Addressed: domain.Addressed{Direct: true},
		Actor:     domain.Actor{ID: "schedule"},
		// The two-phase Eval batches per Thread; a timer without one would never
		// become due. One timer = one stable Thread.
		Threads: []domain.ThreadRef{{Source: "schedule", ID: "schedule:" + c.Definition.ID}},
	}
	if c.Definition.WorkspaceID != "" {
		tr.Scope = domain.WorkspaceScope(c.Definition.WorkspaceID)
		tr.DefaultWorkspace = c.Definition.WorkspaceID
	}
	return tr
}

// scheduledTaskText is what the Ticket says it is. A timer has no author to quote,
// so the text names the timer; the schedule id is stable and readable.
func scheduledTaskText(id string) string {
	return "按定时任务执行：" + id
}

// durStore is the store behind a durable runtime, or nil in memory mode.
func durStore(d *durable) *store.Store {
	if d == nil {
		return nil
	}
	return d.store
}

// logf records optional-capability problems. They are warnings, not failures: a
// broken MCP server must not keep the window from opening.
func logf(format string, args ...any) {
	slog.Warn(fmt.Sprintf("zeokeel: "+format, args...))
}
