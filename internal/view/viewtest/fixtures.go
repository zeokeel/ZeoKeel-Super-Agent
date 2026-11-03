// Package viewtest holds the sample projections the window's tests draw: data
// shaped like what the core publishes, so a page can be tested before it is
// wired. The app never imports it.
package viewtest

import (
	"time"

	"github.com/zeokeel/super-agent/internal/view"
	. "github.com/zeokeel/super-agent/internal/view"
)

// DemoOverview is placeholder content until the store publishes real projections
// (M0-8). It exists so the shell can be seen and tested before the runtime is
// wired, and is removed at M0-8.
func DemoOverview(now time.Time) *OverviewVM {
	return &OverviewVM{
		NeedsPerson: []TicketRow{
			{ID: "42", Title: "修改登录超时配置", Workspace: "acme/webapp", SourceIcon: "github",
				Stage: "等待回答", StageID: StagePersonAnswer, Detail: "等待你确认配置名（ask_user_question）",
				Tone: ToneWarn, Age: 12 * time.Minute,
				Attention: &TicketAttention{Question: "配置项名用 session_timeout 还是 session.timeout？",
					Options: []string{"session_timeout", "session.timeout"}, AskedAt: now.Add(-12 * time.Minute)}},
			{ID: "44", Title: "CI 失败：auth 包测试超时", Workspace: "acme/api", SourceIcon: "github",
				Stage: "需要授权", StageID: StagePersonBlocked, Detail: "受阻：等待授权重跑流水线",
				Tone: ToneDanger, Age: 34 * time.Minute, CanRetry: true},
			{ID: "45", Title: "为 session 配置补迁移脚本", Workspace: "acme/webapp", SourceIcon: "github",
				Stage: "等待评审", StageID: StagePersonReview, Detail: "PR 已就绪，等待你 Review",
				Tone: ToneInfo, Age: 1 * time.Hour},
		},
		Health: []HealthItem{
			{Name: "GitHub Token", Detail: "有效 · 40 天后过期", OK: true},
			{Name: "Feishu 订阅", Detail: "有效 · 3 个群", OK: true},
			{Name: "SQLite", Detail: "健康 · WAL 正常", OK: true},
			{Name: "执行器沙箱", Detail: "工作区可写 · 目录已隔离", OK: true},
			{Name: "MCP notion", Detail: "未连接（可选）", OK: false},
		},
		Activity: demoActivity(),
		Counts:   Counts{Triggers: 186, Evals: 24, Decisions: 9, Tickets: 4},
		Runtime:  Runtime{Running: 1, MaxJobs: 2, Queued: 0, Today: 42, NextPoll: 48 * time.Second},
		Artifacts: []Artifact{
			{Kind: "pr", Title: "PR #128 — 支持 session_timeout", Detail: "+128 −36 · 9 files"},
			{Kind: "report", Title: "report.md — 变更说明", Detail: "4.2 KB · 刚刚"},
			{Kind: "image", Title: "login-timeout.png — 运行截图", Detail: "1440×900 · 3 分钟前"},
		},
		Storage:   Storage{DB: "12.4 MB", Events: 1284, LastBackup: "今日 02:00"},
		UpdatedAt: now,
	}
}

// demoActivity is a plausible day: quiet at night, a burst after lunch.
func demoActivity() []HourCount {
	shape := []int{2, 1, 1, 1, 1, 2, 4, 9, 16, 14, 9, 5,
		3, 4, 6, 12, 18, 26, 9, 7, 5, 4, 3, 2}
	out := make([]HourCount, 0, len(shape))
	for h, n := range shape {
		out = append(out, HourCount{Hour: h, Count: n})
	}
	return out
}

// GroupOf returns one group of a search result, or nil when it has no hits. It is
// the lookup the tests need; the production view keeps only the data.
func GroupOf(vm *view.SearchVM, kind view.SearchKind) *view.SearchGroup {
	if vm == nil {
		return nil
	}
	for i := range vm.Groups {
		if vm.Groups[i].Kind == kind {
			return &vm.Groups[i]
		}
	}
	return nil
}
