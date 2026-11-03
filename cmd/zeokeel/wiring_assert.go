package main

// Compile-time proof that the runtime still satisfies the optional interfaces the
// window probes for. Without these, a renamed method silently disables 回答/重试/
// 停止/暂停 in the panel (fix-ui2 handoff, cross-scope item 10).

import (
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/ui"
)

var (
	_ ui.AttentionAnswerer = (*runtime.Runtime)(nil)
	_ ui.TicketStopper     = (*runtime.Runtime)(nil)
	_ ui.TicketRetrier     = (*runtime.Runtime)(nil)
	_ ui.ExecutionPauser   = (*runtime.Runtime)(nil)
)

// The workspace sidebar needs both creators: without these the "新建工作区/会话"
// buttons silently do nothing (ui2-spec §2).
var (
	_ ui.WorkspaceCreator    = (*runtime.Runtime)(nil)
	_ ui.ConversationCreator = (*runtime.Runtime)(nil)
)

// The panel's 检查/授权 button type-asserts ChannelAuthorizer (im-channel-spec §4).
var _ ui.ChannelAuthorizer = (*runtime.Runtime)(nil)

// 工作区重命名/删除 (ui2 P1-6/P1-7).
var (
	_ ui.WorkspaceRenamer = (*runtime.Runtime)(nil)
	_ ui.WorkspaceDeleter = (*runtime.Runtime)(nil)
)

// UI/UX optional capabilities (interaction audit): model probe, composer target,
// attention count, onboarding skip/demo.
var (
	_ ui.ModelTester         = (*runtime.Runtime)(nil)
	_ ui.WorkspaceDispatcher = (*runtime.Runtime)(nil)
	_ ui.AttentionCounter    = (*runtime.Runtime)(nil)
	_ ui.OnboardingSkipper   = (*runtime.Runtime)(nil)
	_ ui.DemoModer           = (*runtime.Runtime)(nil)
)

// rc2: About/设置 的更新检查（仅查询，不下载）。
var _ ui.UpdateChecker = (*runtime.Runtime)(nil)

// rc2 UI optional capabilities (streaming stop / apply-back / undo / version).
var (
	_ ui.ChatStreamer    = (*runtime.Runtime)(nil)
	_ ui.ArtifactApplier = (*runtime.Runtime)(nil)
	_ ui.RejectUndoer    = (*runtime.Runtime)(nil)
	_ ui.VersionReporter = (*runtime.Runtime)(nil)
)
