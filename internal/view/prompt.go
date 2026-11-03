package view

// The prompt layers the operator can edit from 设置 › 提示词 (design §6.3: 配置
// 动作只在设置页). Router and Worker prompts are separate on purpose: the
// capability difference is expressed by tool allowlists and prompts (design §5.3),
// so editing one must never silently change the other. The eval rules layer is a
// human-readable note: the rules themselves are code + tables, checked by diff,
// not free text (001 / 002).

import "time"

// PromptLayerID names one editable prompt layer. The order of the constants is
// the order the editor shows, so the rail, the save values and the page cannot
// disagree.
type PromptLayerID string

const (
	// PromptRouter is the 研判者 prompt: does this count as work, and which
	// workspace owns it? It never executes and never replies.
	PromptRouter PromptLayerID = "router"
	// PromptWorker is the 执行者 prompt: tools, delivery and verification.
	PromptWorker PromptLayerID = "worker"
	// PromptEvalRules is the explanation of the eval rules shown beside them.
	PromptEvalRules PromptLayerID = "eval_rules"
)

// PromptLayerIDs returns the layers in the order the editor shows them.
func PromptLayerIDs() []PromptLayerID {
	return []PromptLayerID{PromptRouter, PromptWorker, PromptEvalRules}
}

// PromptLayer is one editable layer. Content is what the runtime read; Default is
// the built-in value 恢复默认 resets to. Revision is the configuration revision
// Content was read at: a save carries it, so another window's change is detected
// instead of overwritten (optimistic concurrency).
type PromptLayer struct {
	ID    PromptLayerID
	Label string
	// Note says what this layer decides, in a person's words.
	Note string
	// Content is the persisted prompt, as the core read it.
	Content string
	// Default is the value 恢复默认 writes: the built-in prompt of this build.
	Default string
	// Revision is the configuration revision Content was read at. Zero means
	// the core did not report one.
	Revision uint64
	// Editable is false when the core says this layer cannot be written, as a
	// layer shipped with the binary. ReadOnlyNote then says why.
	Editable     bool
	ReadOnlyNote string
}

// IsDefault reports whether the persisted content is the built-in default.
func (l PromptLayer) IsDefault() bool { return l.Content == l.Default }

// PromptVM is everything the prompt editor draws.
type PromptVM struct {
	Layers []PromptLayer
	// Source names where the values live: "mygo.json".
	Source string
	// Revision is the configuration revision the whole projection was read at.
	// A save returns the new one; per-layer revisions say which layer moved.
	Revision  uint64
	UpdatedAt time.Time
}

// Layer returns one layer by ID, or nil when the core did not project it. A
// missing layer is an empty slot in the editor, never a default written silently.
func (vm *PromptVM) Layer(id PromptLayerID) *PromptLayer {
	if vm == nil {
		return nil
	}
	for i := range vm.Layers {
		if vm.Layers[i].ID == id {
			return &vm.Layers[i]
		}
	}
	return nil
}

// EditableCount returns how many layers can be written. The page shows it so a
// read-only projection is visible before the operator starts typing.
func (vm *PromptVM) EditableCount() int {
	if vm == nil {
		return 0
	}
	n := 0
	for _, l := range vm.Layers {
		if l.Editable {
			n++
		}
	}
	return n
}

// ChangedCount returns how many layers differ from their built-in default, so the
// heading can say "2 层已覆盖" without a second number to keep in sync.
func (vm *PromptVM) ChangedCount() int {
	if vm == nil {
		return 0
	}
	n := 0
	for _, l := range vm.Layers {
		if !l.IsDefault() {
			n++
		}
	}
	return n
}
