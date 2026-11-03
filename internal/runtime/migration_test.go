package runtime

import (
	"path/filepath"
	"testing"

	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// P0-4: a settings file written before the enable switch existed must be
// normalized and stamped once on load, not silently left disabled.
func TestLegacySettingsMigration(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	legacy := map[string]string{
		"source.feishu_enabled":      "true",
		"source.feishu_app_id":       "cli_old",
		"source.feishu_app_secret":   "secret-old",
		"source.dingtalk_app_key":    "ding-old",
		"source.dingtalk_app_secret": "ding-secret-old",
		"source.im_self":             "bot-old",
	}
	if _, err := st.SaveSettings(legacy, 0, false); err != nil {
		t.Fatal(err)
	}

	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	if err := rt.LoadSettings(st); err != nil {
		t.Fatal(err)
	}
	got, err := st.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if got["source.feishu_enabled"] != "1" {
		t.Fatalf("legacy bool not normalized: %q", got["source.feishu_enabled"])
	}
	if got["source.dingtalk_enabled"] != "1" {
		t.Fatalf("legacy DingTalk credentials did not enable the channel: %q", got["source.dingtalk_enabled"])
	}
	if got["source.feishu_transport"] != "ws" {
		// The new default is ws; a legacy longpoll install that had no chat ids
		// would be misconfigured (longpoll requires chats), so ws is the honest
		// migration. Installs with chat ids stay on longpoll.
		t.Fatalf("legacy Feishu transport not pinned: %q", got["source.feishu_transport"])
	}
	if got["source.dingtalk_transport"] != "stream" {
		t.Fatalf("legacy DingTalk transport not pinned: %q", got["source.dingtalk_transport"])
	}
	if got["source.feishu_self_id"] != "bot-old" || got["source.dingtalk_bot_id"] != "bot-old" {
		t.Fatalf("im_self was not split: %+v", got)
	}
	if ver, ok, err := st.SettingsSchema(); err != nil || !ok || ver != store.SettingsSchemaVersion {
		t.Fatalf("schema = %d ok=%v err=%v", ver, ok, err)
	}
}
