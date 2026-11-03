package runtime

// Prompt layer tests: the team's three sections exist and reach the Worker prompt;
// saves are validated (unknown layer/placeholder, over-long, missing required var),
// identical writes are dropped, and history/default-updated detection works.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

func newPromptRuntime(t *testing.T) (*Runtime, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	if err := rt.LoadSettings(st); err != nil {
		t.Fatal(err)
	}
	return rt, st
}

func TestTeamSectionsAreEditableLayers(t *testing.T) {
	rt, st := newPromptRuntime(t)
	defer st.Close()

	vm := rt.promptsVM()
	for _, id := range []view.PromptLayerID{promptCommunication, promptScope, promptDelivery} {
		if vm.Layer(id) == nil {
			t.Fatalf("team layer %q is not projected", id)
		}
	}
	if len(vm.Layers) != len(promptLayerIDs()) {
		t.Fatalf("layers = %d, want %d", len(vm.Layers), len(promptLayerIDs()))
	}

	// The sections reach the Worker instruction.
	cfg := &WorkerConfig{}
	prompt := rt.systemPromptFor(domain.Ticket{}, cfg)
	for _, want := range []string{"沟通方式", "改动范围", "交付标准"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("worker prompt is missing team section %q", want)
		}
	}
}

func TestPromptSaveValidationAndHistory(t *testing.T) {
	rt, st := newPromptRuntime(t)
	defer st.Close()

	rev := uint64(0)
	save := func(values map[string]string) (uint64, error) {
		values["__revision"] = fmt.Sprint(rev)
		next, err := rt.SaveSettings(string(view.SettingsPrompt), values)
		if err == nil {
			rev = next
		}
		return next, err
	}

	if _, err := save(map[string]string{"nonsense": "x"}); err == nil {
		t.Fatal("an unknown layer was accepted")
	}
	if _, err := save(map[string]string{"communication": "用 {{nope}} 说话"}); err == nil {
		t.Fatal("an unknown placeholder was accepted")
	}
	if _, err := save(map[string]string{"worker": "没有占位符的提示"}); err == nil {
		t.Fatal("a worker prompt without required placeholders was accepted")
	}
	if _, err := save(map[string]string{"communication": strings.Repeat("长", maxPromptRunes+1)}); err == nil {
		t.Fatal("an over-long layer was accepted (it must be rejected, not truncated)")
	}
	if _, err := save(map[string]string{"communication": "只写结论，再给证据。"}); err != nil {
		t.Fatalf("a valid team save was refused: %v", err)
	}
	if got := rt.PromptHistory(promptCommunication); len(got) != 1 {
		t.Fatalf("history = %d entries, want 1", len(got))
	}
	// Saving the same text again is a no-op: no second history entry.
	if _, err := save(map[string]string{"communication": "只写结论，再给证据。"}); err != nil {
		t.Fatal(err)
	}
	if got := rt.PromptHistory(promptCommunication); len(got) != 1 {
		t.Fatalf("identical save recorded another version: %d", len(got))
	}
	// Setting a layer back to its exact default clears the override.
	if _, err := save(map[string]string{"communication": promptDefaults()[promptCommunication]}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range rt.promptsVM().Layers {
		if l.ID == promptCommunication && l.Content == l.Default {
			found = true
		}
	}
	if !found {
		t.Fatal("restoring the default did not clear the override")
	}
}

func TestPromptDefaultUpdatedDetection(t *testing.T) {
	rt, st := newPromptRuntime(t)
	defer st.Close()

	if _, err := rt.SaveSettings(string(view.SettingsPrompt), map[string]string{
		"communication": "团队自己的沟通方式", "__revision": "0",
	}); err != nil {
		t.Fatal(err)
	}
	if rt.PromptDefaultUpdated(promptCommunication) {
		t.Fatal("a fresh override must not report a stale default")
	}
	// Simulate a build whose default text changed after the override was saved.
	if err := st.Set(promptBaseKey(promptCommunication), "old-default-hash"); err != nil {
		t.Fatal(err)
	}
	if !rt.PromptDefaultUpdated(promptCommunication) {
		t.Fatal("Default updated was not detected")
	}
	var noteFound bool
	for _, l := range rt.promptsVM().Layers {
		if l.ID == promptCommunication && strings.Contains(l.Note, "内置默认值已更新") {
			noteFound = true
		}
	}
	if !noteFound {
		t.Fatal("the editor note did not mention the updated default")
	}
	_ = time.Now
}
