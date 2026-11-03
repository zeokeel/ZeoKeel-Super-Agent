package agent

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/session"
	"github.com/cloudwego/eino/schema"
)

// repairTornEventLog 裁掉事件日志末尾没有换行的半行。
//
// 官方 FileStore 的行格式是 "<event_id>\t<kind>\t<data>\n"；崩溃可能只写进去
// 半行。append-only 日志的语义允许丢最后一条未完成的记录（它本来就没提交），
// 但不允许整份日志读不出来，所以这里只修这个特定损坏，其余错误原样返回。
func (r *Runner) repairTornEventLog(sessionID string) error {
	// 只有官方文件存储有"半行"这种损坏形态；自定义 store（如 SQLite）自己保证原子性。
	if _, ok := r.store.(*session.FileStore[*schema.Message]); !ok {
		return fmt.Errorf("agent: event store is not file-backed")
	}
	path := filepath.Join(r.cfg.SessionDir, url.PathEscape(sessionID)+".evlog")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		// 以换行结尾说明损坏不是"半行"，不要动它。
		return fmt.Errorf("agent: event log is not torn")
	}
	cut := bytes.LastIndexByte(data, '\n')
	if cut < 0 {
		return fmt.Errorf("agent: event log has no complete record")
	}
	return os.WriteFile(path, data[:cut+1], 0o644)
}

// compactEventKind 是压缩事件在事件日志里的扩展 kind。用 adk 官方的扩展前缀，
// 未知 kind 的读取方会安全忽略；同时面板/统计能据此还原"压缩发生过几次"。
const compactEventKind = adk.SessionEventKind("x.zeokeel.compact")

// interruptedToolNote 是补进日志的工具结果内容：模型看到它就知道"这个工具可能
// 已经执行过一部分"，不会把中断当成"工具没跑过"直接重试（design §5.8）。
const interruptedToolNote = "interrupted: 该工具调用在进程中断时还没有返回结果，可能已经部分执行" +
	"（例如文件被写入了一半）。不要假设它没发生——先检查工作区当前状态，再决定是否重试。"

// patchInterruptedToolCalls 给日志里"有调用、无结果"的工具调用补一条 interrupted
// 工具消息，并把补了几条返回。
//
// 为什么必须补：真实 provider 会拒绝"assistant 带 tool_calls 但后面没有对应 tool
// 消息"的历史；不补的话恢复运行的第一句话就会 400，崩溃恢复等于没做。
func (r *Runner) patchInterruptedToolCalls(ctx context.Context, sessionID string,
	events []*adk.SessionEvent[*schema.Message]) (int, error) {
	open := unpairedToolCalls(events)
	if len(open) == 0 {
		return 0, nil
	}
	ids := make([]string, 0, len(open))
	for id := range open {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	patches := make([]*adk.SessionEvent[*schema.Message], 0, len(ids))
	for _, id := range ids {
		eventID, err := newSessionEventID()
		if err != nil {
			return 0, err
		}
		patches = append(patches, &adk.SessionEvent[*schema.Message]{
			EventID:   eventID,
			Timestamp: time.Now().UTC(),
			Kind:      adk.SessionEventMessage,
			Message:   schema.ToolMessage(interruptedToolNote, id),
		})
	}
	if err := r.store.AppendEvents(ctx, sessionID, patches); err != nil {
		return 0, fmt.Errorf("agent: patch interrupted tool calls: %w", err)
	}
	return len(patches), nil
}

// unpairedToolCalls 返回日志里没有对应工具结果的调用（callID -> 工具名）。
// 只看 message 事件：span 事件只是给面板/统计用的，恢复语义以消息为准。
func unpairedToolCalls(events []*adk.SessionEvent[*schema.Message]) map[string]string {
	open := map[string]string{}
	for _, ev := range events {
		if ev == nil || ev.Kind != adk.SessionEventMessage || ev.Message == nil {
			continue
		}
		switch ev.Message.Role {
		case schema.Assistant:
			for _, call := range ev.Message.ToolCalls {
				if call.ID != "" {
					open[call.ID] = call.Function.Name
				}
			}
		case schema.Tool:
			delete(open, ev.Message.ToolCallID)
		}
	}
	return open
}

// recoveryTask 是恢复运行时发给模型的那句话。它要把"可能已部分执行"说清楚，
// 否则模型会按下一次干净的重试来处理，重复的副作用就出现了。
func recoveryTask(patched int) string {
	if patched > 0 {
		return "上一次运行在工具执行中途中断了。没有结果的工具调用已经被标记为 interrupted，" +
			"它们可能已经部分执行过。先检查工作区当前状态，判断哪些工作已经完成，再继续原任务；" +
			"如果原任务已经完成，直接给出最终答复。"
	}
	return "上一次运行中断了。先检查工作区当前状态，再继续完成原任务；" +
		"如果原任务已经完成，直接给出最终答复。"
}

// computesessionStats 从事件日志算出面板指标。事件日志是唯一事实来源，
// 所以统计不需要任何内存状态，重启后照样能算（design §5.4 的可观测性）。
func computesessionStats(sessionID string, events []*adk.SessionEvent[*schema.Message]) sessionStats {
	stats := sessionStats{SessionID: sessionID}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		switch ev.Kind {
		case adk.SessionEventSessionStatusRunning:
			stats.Turns++
		case adk.SessionEventMessagesReplaced, compactEventKind:
			stats.Compactions++
		case adk.SessionEventMessage:
			if ev.Message == nil {
				continue
			}
			stats.Messages++
			if ev.Message.Role == schema.Assistant && ev.Message.Content != "" {
				stats.LastAssistant = ev.Message.Content
			}
		}
		if ev.Lifecycle != nil {
			// 以最后一条生命周期事件为准：崩溃的会话会停在 running 上，"日志
			// 末尾是不是 idle"才是面板要显示的"上次是否正常收尾"。
			stats.Idle = ev.Lifecycle.State == adk.SessionRunStateIdle
		}
	}
	// 用量优先从 assistant 消息的 ResponseMeta 累计：adk 的 model span 并非每轮
	// 都落 end 事件（最后一轮常常没有），只信 span 会少算一整次调用。
	if !addUsageFromMessages(&stats, events) {
		addUsageFromSpans(&stats, events)
	}
	stats.Interrupted = len(unpairedToolCalls(events))
	return stats
}

// addUsageFromMessages 从持久化的 assistant 消息里累计用量；一条都拿不到时返回 false，
// 让调用方退回 span。
func addUsageFromMessages(stats *sessionStats, events []*adk.SessionEvent[*schema.Message]) bool {
	found := false
	for _, ev := range events {
		if ev == nil || ev.Message == nil || ev.Message.Role != schema.Assistant {
			continue
		}
		meta := ev.Message.ResponseMeta
		if meta == nil || meta.Usage == nil {
			continue
		}
		found = true
		stats.PromptTokens += meta.Usage.PromptTokens
		stats.CompletionTokens += meta.Usage.CompletionTokens
		if meta.Usage.TotalTokens > 0 {
			stats.TotalTokens += meta.Usage.TotalTokens
		} else {
			stats.TotalTokens += meta.Usage.PromptTokens + meta.Usage.CompletionTokens
		}
	}
	return found
}

// addUsageFromSpans 从 model span 的结束事件累计用量，兼容没有消息用量的老日志。
func addUsageFromSpans(stats *sessionStats, events []*adk.SessionEvent[*schema.Message]) {
	for _, ev := range events {
		if ev == nil || ev.Kind != adk.SessionEventSpanModelRequestEnd ||
			ev.Span == nil || ev.Span.Model == nil || ev.Span.Model.Usage == nil {
			continue
		}
		u := ev.Span.Model.Usage
		stats.PromptTokens += u.InputTokens
		stats.CompletionTokens += u.OutputTokens
		if u.Raw != nil && u.Raw.TotalTokens > 0 {
			stats.TotalTokens += u.Raw.TotalTokens
		} else {
			stats.TotalTokens += u.InputTokens + u.OutputTokens
		}
	}
}
