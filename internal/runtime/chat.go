package runtime

// Super-Agent chat (P0-7): the global input is a conversation, not only a
// dispatcher. Every message gets a visible answer:
//
//   - work dispatched  → a system outcome "已派发 #N" (no red error);
//   - proposal/question → a system outcome saying a person must confirm;
//   - no work          → an assistant answer (model when configured, a
//     deterministic workspace/next-steps answer otherwise).
//
// History is bounded to chatHistoryMax and published as view.ChatVM.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/assess"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// chatHistoryMax bounds the in-memory conversation (ui2 P0-7: ≤20).
const chatHistoryMax = 20

// chatHistory returns the bounded conversation.
func (r *Runtime) chatHistory() []view.ChatMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]view.ChatMessage, len(r.chat))
	copy(out, r.chat)
	return out
}

func (r *Runtime) chatVM() *view.ChatVM {
	msgs := r.chatHistory()
	return &view.ChatVM{Messages: msgs, UpdatedAt: r.clock()}
}

// appendChat adds one line, dropping the oldest beyond the bound.
func (r *Runtime) appendChat(role view.ChatRole, kind view.ChatKind, text string) {
	stored := store.ChatMessage{ID: string(domain.Key(domain.Global, "chat", fmt.Sprint(r.clock().UnixNano()))),
		Role: string(role), Text: text, At: r.clock()}
	r.mu.Lock()
	r.chat = append(r.chat, view.ChatMessage{Role: role, Kind: kind, Text: text, At: stored.At})
	if len(r.chat) > chatHistoryMax {
		r.chat = r.chat[len(r.chat)-chatHistoryMax:]
	}
	st := r.settingsStore
	r.mu.Unlock()
	if st != nil {
		if _, err := st.AppendChat("superagent", stored); err != nil {
			r.slogger().Warn("chat message could not be persisted", "error", err)
		}
	}
}

// answerChat produces the assistant's answer for a message that produced no work:
// one model call when a model is configured, otherwise a deterministic answer
// that says what is missing and what to do next.
func (r *Runtime) answerChat(text string) string {
	if base := r.chatSystemPrompt(text); base != "" {
		if cfg := r.workerConfig(); cfg != nil && cfg.BaseURL != "" && cfg.Model != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			gen, err := assess.NewEinoGenerator(ctx, assess.EinoConfig{
				BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: cfg.Model,
			})
			if err == nil {
				answer, gerr := gen.Generate(ctx, chatSystemBase, base)
				if gerr == nil && strings.TrimSpace(answer) != "" {
					return strings.TrimSpace(answer)
				}
			}
		}
	}
	// Deterministic fallback: workspace list when asked, otherwise next steps, and
	// say plainly that the model is not configured.
	if list, ok := r.workspaceAnswer(text); ok {
		return list + "\n（未配置模型，以上由本机规则回答。）"
	}
	return "这次没有派发工单：没有匹配到明确的工作请求。" +
		"\n你可以：① 在左侧选择一个工作区「新建会话」，把任务交给它；" +
		"② 或直接说明要做什么（例如「在 webapp 里改登录超时」）。" +
		"\n（未配置模型，以上由本机规则回答；配置模型后我会直接回答。）"
}

// chatSystemPrompt builds the user turn for the chat model: the person's message
// plus the facts the model needs (workspaces, open work) so it can answer without
// another round trip.
func (r *Runtime) chatSystemPrompt(text string) string {
	var b strings.Builder
	b.WriteString("工作区：\n")
	for _, w := range r.eng.Workspaces() {
		b.WriteString("- ")
		b.WriteString(w.Name)
		if w.Brief.Purpose != "" {
			b.WriteString("（")
			b.WriteString(w.Brief.Purpose)
			b.WriteString("）")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n用户消息：")
	b.WriteString(text)
	return b.String()
}

// chatSystemBase is the concise persona; it is not the Worker prompt.
const chatSystemBase = `你是 ZeoKeel 超级Agent。你要么把工作派给合适的工作区并说明，要么直接、简短地回答操作员。
- 用中文，结论先行，不要客套。
- 你可以看到工作区列表；不要编造不存在的工作区。
- 如果消息其实是工作任务，建议选择哪个工作区并说明如何派发；不要假装已经派发。`
