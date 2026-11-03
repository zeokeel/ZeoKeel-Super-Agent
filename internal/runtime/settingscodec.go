package runtime

// Settings codec (P0-1/P0-4): one bool encoding at the boundary. The UI writes
// switches as "1"/"0"; older files and hand-edited settings may hold
// true/false/yes/no. ParseSettingBool accepts both, EncodeSettingBool writes the
// canonical form, and LoadSettings normalizes every known bool key once.

import (
	"sort"
	"strings"

	"github.com/zeokeel/super-agent/internal/store"
)

// ParseSettingBool parses one switch value. ok is false when the text is not a
// recognized boolean (callers decide whether that means "absent" or "invalid").
func ParseSettingBool(raw string) (value, ok bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "enabled":
		return true, true
	case "0", "false", "no", "off", "disabled":
		return false, true
	}
	return false, false
}

// EncodeSettingBool is the only bool encoding the runtime writes.
func EncodeSettingBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// settingsSchemaKey records that the one-shot migration ran. The value is not
// used for branching beyond "was it migrated before": later schema versions can
// add their own one-shot steps keyed off the version string.
// settingsSchemaVersion mirrors store.SettingsSchemaVersion. Chat and the IM
// enable tri-state were introduced together.
const settingsSchemaVersion = 1

// boolSettingKeys are normalized on load. Only keys the runtime itself reads.
var boolSettingKeys = []string{
	"source.feishu_enabled",
	"source.dingtalk_enabled",
	"execution.plan_tasks",
	"execution.agents_md",
	"execution.web_tools",
}

// secretSettingSuffixes identify values that must never be echoed back to the UI
// or copied into status/health.
var secretSettingSuffixes = []string{"_secret", "_app_secret", "_api_key", "api_key", "password", "token"}

// IsSecretSettingKey reports whether a settings key holds a credential.
func IsSecretSettingKey(key string) bool {
	lower := strings.ToLower(key)
	for _, suffix := range secretSettingSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// settingBool reads one bool setting with a default when absent/unparseable.
func (r *Runtime) settingBool(key string, def bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := ParseSettingBool(r.settings[key]); ok {
		return v
	}
	return def
}

// migrateSettings normalizes bool encodings and applies the one-shot IM enable
// migration: a deployment that had credentials before the switch existed is
// enabled explicitly, so it does not silently stop after an upgrade.
//
// It returns the keys it changed; the caller persists them and adopts the new
// map. Flag-only deployments are handled by BootstrapIMFlags after LoadSettings,
// because flags are not visible here.
func migrateSettings(values map[string]string) map[string]string {
	changed := map[string]string{}
	for _, key := range boolSettingKeys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		if val, parsed := ParseSettingBool(raw); parsed {
			if canon := EncodeSettingBool(val); canon != raw {
				changed[key] = canon
			}
		}
	}
	// IM enable migration: only when the key was never written. Credentials in the
	// file are the evidence that this channel used to work.
	if _, ok := values["source.feishu_enabled"]; !ok && feishuCredsComplete(values) {
		changed["source.feishu_enabled"] = "1"
	}
	// A pre-ws Feishu install used long polling. New installs default to the
	// official larkws connection, but an upgrade must not silently change the
	// receive path of an already-working channel: pin the transport it used.
	if _, ok := values["source.feishu_transport"]; !ok && feishuCredsComplete(values) {
		changed["source.feishu_transport"] = "longpoll"
	}
	if _, ok := values["source.feishu_transport"]; !ok && feishuCredsComplete(values) {
		// New transport default is ws; a legacy install that had long-poll chat
		// ids keeps longpoll. Both keep working without a misconfigured warning.
		if strings.TrimSpace(values["source.feishu_chats"]) != "" {
			changed["source.feishu_transport"] = "longpoll"
		} else {
			changed["source.feishu_transport"] = "ws"
		}
	}
	if _, ok := values["source.dingtalk_transport"]; !ok {
		// The pre-switch build only had Stream. A legacy install with stream
		// credentials must keep working instead of suddenly probing a missing
		// `dws` binary, so pin the transport it actually used.
		if strings.TrimSpace(values["source.dingtalk_app_key"]) != "" &&
			strings.TrimSpace(values["source.dingtalk_app_secret"]) != "" {
			changed["source.dingtalk_transport"] = "stream"
		}
	}
	if _, ok := values["source.dingtalk_enabled"]; !ok {
		// Only credential evidence enables: DWS-managed installs must stay opt-in
		// (otherwise every fresh machine reports "dws 未安装").
		if dingCredsComplete(values) || legacyDingCreds(values) {
			changed["source.dingtalk_enabled"] = "1"
		}
	}
	// New per-channel self id: the old im_self applied to both channels; copy it
	// to whichever channel has no id of its own.
	if old := strings.TrimSpace(values["source.im_self"]); old != "" {
		if _, ok := values["source.feishu_self_id"]; !ok {
			changed["source.feishu_self_id"] = old
		}
		if _, ok := values["source.dingtalk_bot_id"]; !ok {
			changed["source.dingtalk_bot_id"] = old
		}
	}
	// DWS intent evidence: a custom dws command or explicit DWS profile/listen
	// config means this install already uses DWS. Pin the transport so an upgrade
	// does not reinterpret it as stream.
	if _, ok := values["source.feishu_transport"]; !ok && feishuCredsComplete(values) {
		// New transport default is ws; a legacy install that had long-poll chat
		// ids keeps longpoll. Both keep working without a misconfigured warning.
		if strings.TrimSpace(values["source.feishu_chats"]) != "" {
			changed["source.feishu_transport"] = "longpoll"
		} else {
			changed["source.feishu_transport"] = "ws"
		}
	}
	if _, ok := values["source.dingtalk_transport"]; !ok {
		dwsIntent := values["source.dingtalk_profile"] != "" ||
			values["source.dingtalk_chat_query"] != "" ||
			(values["source.dingtalk_dws_cmd"] != "" && values["source.dingtalk_dws_cmd"] != "dws")
		if dwsIntent {
			changed["source.dingtalk_transport"] = "dws"
		}
	}
	return changed
}

func feishuCredsComplete(values map[string]string) bool {
	return strings.TrimSpace(values["source.feishu_app_id"]) != "" &&
		strings.TrimSpace(values["source.feishu_app_secret"]) != ""
}

func dingCredsComplete(values map[string]string) bool {
	transport := strings.TrimSpace(values["source.dingtalk_transport"])
	if transport == "" || transport == "stream" {
		return strings.TrimSpace(values["source.dingtalk_app_key"]) != "" &&
			strings.TrimSpace(values["source.dingtalk_app_secret"]) != ""
	}
	return strings.TrimSpace(values["source.dingtalk_app_key"]) != ""
}

// legacyDingCreds is the pre-switch evidence: stream-era credentials without a
// transport key. Kept separate so DWS installs are never auto-enabled by accident.
func legacyDingCreds(values map[string]string) bool {
	return strings.TrimSpace(values["source.dingtalk_robot"]) != "" &&
		strings.TrimSpace(values["source.dingtalk_app_key"]) != "" &&
		strings.TrimSpace(values["source.dingtalk_app_secret"]) != ""
}

// persistSettingChanges writes migrated keys through the store. Key-by-key writes
// are enough: the migration runs once, at boot, before the window can save.
func persistSettingChanges(st *store.Store, values, changed map[string]string) error {
	keys := make([]string, 0, len(changed))
	for k := range changed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		values[k] = changed[k]
		if st != nil {
			if err := st.Set(k, changed[k]); err != nil {
				return err
			}
		}
	}
	return nil
}

// BootstrapIMFlags applies the P0-4 flag rule: a deployment configured only on
// the command line (no stored *_enabled) must be enabled when its credentials
// are complete. Explicit "0" always wins. Call after LoadSettings, before
// starting the IM manager.
func (r *Runtime) BootstrapIMFlags(f IMFlags) {
	r.mu.Lock()
	values := make(map[string]string, len(r.settings))
	for k, v := range r.settings {
		values[k] = v
	}
	st := r.settingsStore
	r.mu.Unlock()

	changed := map[string]string{}
	if _, ok := values["source.feishu_enabled"]; !ok {
		appID := firstNonEmpty(f.FeishuAppID, values["source.feishu_app_id"])
		secret := firstNonEmpty(f.FeishuAppSecret, values["source.feishu_app_secret"])
		if appID != "" && secret != "" {
			changed["source.feishu_enabled"] = "1"
		}
	}
	if _, ok := values["source.dingtalk_enabled"]; !ok {
		key := firstNonEmpty(f.DingAppKey, values["source.dingtalk_app_key"])
		secret := firstNonEmpty(f.DingAppSecret, values["source.dingtalk_app_secret"])
		if key != "" && secret != "" {
			changed["source.dingtalk_enabled"] = "1"
		}
	}
	if len(changed) == 0 {
		return
	}
	if err := persistSettingChanges(st, values, changed); err != nil {
		r.slogger().Warn("IM flag enable migration could not be persisted", "error", err)
	}
	r.mu.Lock()
	for k, v := range changed {
		r.settings[k] = v
	}
	r.mu.Unlock()
	r.Publish()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
