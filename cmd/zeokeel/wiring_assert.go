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
