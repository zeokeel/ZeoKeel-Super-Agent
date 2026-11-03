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
