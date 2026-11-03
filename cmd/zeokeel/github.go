package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/sources"
	_ "github.com/zeokeel/super-agent/internal/sources/github" // registers the kind
	"github.com/zeokeel/super-agent/internal/view"
)

// githubOptions is what the command line knows about the GitHub Source.
type githubOptions struct {
	Repo   string
	Login  string
	CLI    string
	Self   []string
	Poll   time.Duration
	Cancel context.CancelFunc
}

// startGitHub starts polling one repository. It is opt-in: without -repo nothing
// listens, and the 来源 page says so instead of showing an empty table (design
// §4.7 修正 1: a GitHub Source is workspace-scoped and watches one repository).
func startGitHub(rt *runtime.Runtime, sender *sourceSender, o githubOptions) error {
	cfg, err := json.Marshal(map[string]any{
		"repository": o.Repo,
		"login":      o.Login,
		"cli":        o.CLI,
		"self":       o.Self,
	})
	if err != nil {
		return err
	}
	kind, err := sources.New("github", cfg)
	if err != nil {
		return err
	}
	// The same instance both polls and replies, so the reply carries the marker the
	// poller's cursor logic expects.
	if sender != nil {
		sender.setKinds("github", kind)
	}
	poller, ok := kind.(sources.Poller)
	if !ok {
		return fmt.Errorf("github Source does not support polling")
	}

	ctx, cancel := context.WithCancel(context.Background())
	o.Cancel = cancel
	cursorKey := "github:" + o.Repo
	go func() {
		// Resume from the persisted token: a restart continues where the last
		// successful round stopped instead of replaying (or skipping) history.
		cursor := sources.Cursor(rt.Cursor(cursorKey))
		tick := time.NewTicker(o.Poll)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			started := time.Now()
			res, err := poller.Poll(ctx, sources.PollRequest{Cursor: cursor},
				func(t domain.Trigger) error {
					// A PR/issue close is a fact even when the trigger itself is
					// filtered (bot/self): close the followed Thread, which ends the
					// Tickets finished by it with merged/closed (003:96-106).
					if _, err := rt.CloseThread(t); err != nil {
						return err
					}
					if _, err := rt.ReceiveTrigger(t); err != nil && !errors.Is(err, engine.ErrDuplicate) {
						return err
					}
					return nil
				})
			if err != nil {
				rt.SetSourceStatus(runtime.SourceStatus{
					Kind: "github", Name: o.Repo, Account: o.Login, Enabled: true,
					Facts: []view.Fact{
						{Label: "仓库", Value: o.Repo},
						{Label: "账号", Value: o.Login},
					},
					At: started,
					Last: view.PollResult{At: started, Outcome: view.PollFailed,
						Problem: err.Error(), Detail: "轮询失败，检查 gh 是否登录"},
				})
				continue
			}
			// The cursor advances only when every message this round was taken in;
			// a partial round replays and content-derived ids drop the duplicates.
			cursor = res.Next
			if string(res.Next) != "" {
				if err := rt.SetCursor(cursorKey, string(res.Next)); err != nil {
					logf("游标保存失败（本进程继续，重启会重放一轮）: %v", err)
				}
			}
			outcome := view.PollOK
			if res.Unreadable > 0 {
				outcome = view.PollPartial
			}
			rt.SetSourceStatus(runtime.SourceStatus{
				Kind: "github", Name: o.Repo, Account: o.Login, Enabled: true,
				Facts: []view.Fact{
					{Label: "仓库", Value: o.Repo},
					{Label: "账号", Value: o.Login},
				},
				Cursor: string(res.Next), At: started,
				Last: view.PollResult{
					At: started, Outcome: outcome,
					Detail: fmt.Sprintf("收到 %d 条，过滤 %d 条，无法解析 %d 条",
						res.Received, res.Filtered, res.Unreadable),
				},
			})
		}
	}()
	return nil
}
