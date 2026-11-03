package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIsSecretKey(t *testing.T) {
	cases := map[string]bool{
		// Documented secret keys (design review P1-9): must never leave the process.
		"setting:source.feishu_app_secret":   true,
		"setting:source.dingtalk_app_secret": true,
		"setting:onboard.api_key":            true,
		"worker.api_key":                     true,
		"setting:github_token":               true,
		"execution.private_key":              true,
		"execution.some_key":                 true,
		"setting:onboard.api_key_":           true,
		"setting:Source.Authorization":       true,
		// Non-secret configuration and bookkeeping: redacting these would hide
		// status facts for no security gain.
		"setting:source.feishu_app_id":     false,
		"setting:onboard.model":            false,
		"setting:onboard.base_url":         false,
		"setting:source.feishu_chats":      false,
		"setting:people.allowlist":         false,
		"setting:execution.concurrency":    false,
		"schema":                           false,
		"setting.__revision":               false,
		"setting.__schema":                 false,
		"source.cursor.github:acme/webapp": false,
		"chat.dm:ding_zhangsan":            false,
		"setup.complete":                   false,
		"":                                 false,
	}
	for key, want := range cases {
		if got := IsSecretKey(key); got != want {
			t.Errorf("IsSecretKey(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestRedactText(t *testing.T) {
	cases := []struct{ in, want, notWant string }{
		{"dws auth: access_token=abc123 expired", "abc123", ""},
		{"request failed: token = 'xyz.987'", "xyz.987", ""},
		{"Authorization: Bearer eyJhbGciOi.abc", "eyJhbGciOi", ""},
		{"server said api_key=sk-live-1 rejected", "sk-live-1", ""},
		// Prose that merely mentions the words must survive: redaction is keyed on
		// a name=value / name: value shape, not on the word alone.
		{"需要重新授权：HTTP 401（token expired）", "", "token expired"},
		{"no credentials here, just text", "", "no credentials here"},
		{"auth status failed", "", "auth status failed"},
	}
	for _, c := range cases {
		got := redactText(c.in)
		if c.notWant == "" && !strings.Contains(got, RedactedValue) {
			t.Errorf("redactText(%q) = %q, want it to contain %q", c.in, got, RedactedValue)
		}
		if c.notWant != "" {
			if !strings.Contains(got, c.notWant) {
				t.Errorf("redactText(%q) = %q, want it to preserve %q", c.in, got, c.notWant)
			}
			if strings.Contains(got, RedactedValue) && !strings.Contains(c.in, "token") {
				// no secret shape in these inputs; nothing should be redacted
				t.Errorf("redactText(%q) = %q, redacted a non-secret sentence", c.in, got)
			}
		}
	}
	if strings.Contains(redactText("dws auth: access_token=abc123 expired"), "abc123") {
		t.Fatal("a value after a secret-shaped name was not redacted")
	}
}

// Inspect is the surface a `zeokeel status` or a support bundle prints, so it must
// never return a credential — neither as a KV value nor inside a health detail —
// while the writer-side accessors keep working.
func TestInspectNeverReturnsSecrets(t *testing.T) {
	s, path := openTemp(t)
	secrets := map[string]string{
		"setting:onboard.api_key":            "sk-live-VERYSECRET1",
		"setting:source.feishu_app_secret":   "fs-VERYSECRET2",
		"setting:source.dingtalk_app_secret": "dt-VERYSECRET3",
		"setting:github_token":               "ghp-VERYSECRET4",
		"execution.private_key":              "pk-VERYSECRET5",
	}
	for k, v := range secrets {
		if err := s.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	plain := map[string]string{
		"setting:onboard.model":            "deepseek-chat",
		"setting:source.feishu_app_id":     "cli_public_ok",
		"setting:execution.concurrency":    "3",
		"schema":                           "3",
		"setup.complete":                   "1",
		"source.cursor.github:acme/webapp": "2026-01-01T00:00:00.000000000Z",
		"chat.dm:1":                        `[{"role":"user","text":"hi"}]`,
	}
	for k, v := range plain {
		if err := s.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	if _, err := s.Exec(`INSERT INTO health(entry,state,detail,at) VALUES('im:dingtalk','error',?,?)`,
		"dws auth: access_token=HEALTHSECRET6 expired", FormatTime(now)); err != nil {
		t.Fatal(err)
	}

	view, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range secrets {
		if got := view.KV[k]; got != RedactedValue {
			t.Fatalf("Inspect KV[%q] = %q, want %q", k, got, RedactedValue)
		}
		if strings.Contains(allViewText(t, view), v) {
			t.Fatalf("Inspect leaked the secret value of %q", k)
		}
	}
	for k, want := range plain {
		if got := view.KV[k]; got != want {
			t.Fatalf("Inspect KV[%q] = %q, want the non-secret value %q", k, got, want)
		}
	}
	if len(view.Health) != 1 {
		t.Fatalf("health = %+v", view.Health)
	}
	if got := view.Health[0].Detail; strings.Contains(got, "HEALTHSECRET6") {
		t.Fatalf("Inspect health detail leaked a credential: %q", got)
	} else if !strings.Contains(got, RedactedValue) {
		t.Fatalf("Inspect health detail was not redacted: %q", got)
	}

	// The writer-side accessors still return the real values: the runtime needs them.
	values, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if got := values["onboard.api_key"]; got != "sk-live-VERYSECRET1" {
		t.Fatalf("Settings()[onboard.api_key] = %q, want the real value", got)
	}
	if got, ok, err := s.Get("setting:source.feishu_app_secret"); err != nil || !ok || got != "fs-VERYSECRET2" {
		t.Fatalf("Get returned %q ok=%v err=%v", got, ok, err)
	}
}

func allViewText(t *testing.T, view HealthView) string {
	t.Helper()
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// HealthEntries is the writer-side health read; it is used by the same status /
// panel code paths after a reload, so it scrubs details too.
func TestHealthEntriesRedactsDetails(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	if _, err := s.Exec(`INSERT INTO health(entry,state,detail,at) VALUES('effect:reply','error',?,?)`,
		"send failed: password=hunter2", FormatTime(now)); err != nil {
		t.Fatal(err)
	}
	entries, err := s.HealthEntries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %+v err=%v", entries, err)
	}
	if strings.Contains(entries[0].Detail, "hunter2") {
		t.Fatalf("HealthEntries leaked a credential: %q", entries[0].Detail)
	}
}

// The status projection reads specific non-secret kv keys. Redaction must be
// selective: hiding a configuration fact status needs would be a regression in the
// other direction (this test pins the keys runtime/status.go actually reads).
func TestStatusFactsSurviveRedaction(t *testing.T) {
	s, path := openTemp(t)
	facts := map[string]string{
		"runtime.manually_stopped":         "1", // runtime.ManuallyStoppedKey
		"setup.complete":                   "1",
		"setting:onboard.done":             "1",
		"setting:onboard.model":            "deepseek-chat",
		"setting:execution.concurrency":    "3",
		"source.cursor.github:acme/webapp": "2026-01-01T00:00:00.000000000Z",
		"schema":                           "3",
	}
	for k, v := range facts {
		if err := s.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	// And one secret alongside them, to prove the selective rule.
	if err := s.Set("setting:onboard.api_key", "sk-should-not-appear"); err != nil {
		t.Fatal(err)
	}

	view, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range facts {
		if got := view.KV[k]; got != want {
			t.Fatalf("status fact %q = %q, want %q (redaction must be selective)", k, got, want)
		}
	}
	if got := view.KV["setting:onboard.api_key"]; got != RedactedValue {
		t.Fatalf("secret = %q, want %q", got, RedactedValue)
	}
	if strings.Contains(allViewText(t, view), "sk-should-not-appear") {
		t.Fatal("the status view leaked the API key")
	}
}
