package view

// 超级Agent 的对话：a bounded conversation between the operator and the agent.
// The agent answers questions here and records what it did with a task ("已派发
// #42", "需要你确认"); the panel draws it on 总览 and shows the latest line above
// the composer on every page, so a message never disappears into another page.

import "time"

// ChatRole is who said a line.
type ChatRole string

const (
	ChatUser      ChatRole = "user"      // the operator typed it
	ChatAssistant ChatRole = "assistant" // 超级Agent answered
	ChatSystem    ChatRole = "system"    // the runtime recorded an outcome
)

// ChatKind says what a line is, for colour: only ChatError is a failure and may
// be red. "没有派发工单，因为…" is a ChatAnswer/ChatNote, not an error: the message
// was answered, it just did not become work.
type ChatKind string

const (
	ChatOutcome ChatKind = "outcome" // 已派发 #42 / 需要你确认 / 已在执行
	ChatAnswer  ChatKind = "answer"  // an answer to a question
	ChatNote    ChatKind = "note"    // a neutral remark (asked back, filtered…)
	ChatError   ChatKind = "error"   // a genuine failure: network, database, permission
)

// ChatMessage is one line of the conversation.
type ChatMessage struct {
	Role ChatRole
	Kind ChatKind
	Text string
	At   time.Time
}

// ChatVM is the conversation history: bounded by the runtime (it keeps the most
// recent lines), oldest first.
type ChatVM struct {
	Messages  []ChatMessage
	UpdatedAt time.Time
	// Streaming is true while the newest assistant line is still arriving: the
	// panel draws a typing cursor and offers 停止 instead of pretending the
	// answer is complete.
	Streaming bool
}

// Tail returns the last n messages, oldest first. n <= 0 returns none.
func (vm *ChatVM) Tail(n int) []ChatMessage {
	if vm == nil || n <= 0 || len(vm.Messages) == 0 {
		return nil
	}
	if n > len(vm.Messages) {
		n = len(vm.Messages)
	}
	return vm.Messages[len(vm.Messages)-n:]
}

// Last returns the newest message.
func (vm *ChatVM) Last() (ChatMessage, bool) {
	if vm == nil || len(vm.Messages) == 0 {
		return ChatMessage{}, false
	}
	return vm.Messages[len(vm.Messages)-1], true
}

// LastAnswer returns the newest line the agent side said (assistant or system),
// which is what the composer shows above it. It skips the operator's own lines:
// echoing what was just typed is not a result.
func (vm *ChatVM) LastAnswer() (ChatMessage, bool) {
	if vm == nil {
		return ChatMessage{}, false
	}
	for i := len(vm.Messages) - 1; i >= 0; i-- {
		if vm.Messages[i].Role != ChatUser {
			return vm.Messages[i], true
		}
	}
	return ChatMessage{}, false
}

// IsFailure reports whether the message is a genuine failure, the only kind the
// panel may paint red.
func (m ChatMessage) IsFailure() bool { return m.Kind == ChatError }
