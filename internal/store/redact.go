package store

// Read-side secret redaction.
//
// The store keeps settings in the same kv table as everything else, so a raw read
// of kv (settings, cursors, schema stamp, prompt revisions) contains app secrets
// and provider API keys. The writer-side accessors (Settings, Get) must return the
// real values because the runtime needs them to talk to providers; the *inspection*
// surface — what `zeokeel status`, a backup check or a debug dump prints — must
// never return them. Everything in Inspect/HealthEntries therefore goes through
// this file's redaction.
//
// This is deliberately key-based (and, for health detail strings, pattern-based):
// guessing a secret from a value's shape is unreliable, while the key names in
// this application are stable and documented in the design (source.*_app_secret,
// onboard.api_key, worker.api_key, execution.*_key, github_token, ...).

import (
	"regexp"
	"strings"
)

// RedactedValue replaces a secret's value in a read-only inspection view. The
// value is kept visible (not omitted) so a caller can still tell "configured"
// from "not configured" without learning the secret.
const RedactedValue = "***"

// secretKeyPattern matches the key names whose values must never leave the
// process. It intentionally errs on the side of redaction: a false positive only
// hides a configuration value from status, while a false negative leaks a
// credential into a terminal or a log file.
var secretKeyPattern = regexp.MustCompile(`(?i)(secret|token|password|passwd|api[_-]?key|apikey|private[_-]?key|credential|authorization|auth)`)

// bookkeepingKey reports kv keys the store owns (schema stamps, revisions). They
// are not settings and are never secret; Settings() and SaveSettings() must not
// treat them as user settings.
func bookkeepingKey(key string) bool {
	return strings.HasPrefix(key, "__")
}

// IsSecretKey reports whether a kv/settings key names a value that must be
// redacted in read-only inspection views. Keys are matched case-insensitively on
// the documented secret-ish words, so "setting:source.feishu_app_secret",
// "setting:onboard.api_key", "worker.api_key", "setting:github_token" and
// "execution.private_key" are all redacted, while "setting:source.feishu_app_id",
// "setting:onboard.model" and "source.cursor.github:o/r" are not.
//
// Store schema/version bookkeeping keys are never secret, even though a caller
// may append a suffix: the store owns the "__" namespace.
func IsSecretKey(key string) bool {
	if key == "" {
		return false
	}
	if strings.HasPrefix(key, "__") || strings.HasSuffix(key, "__schema") || strings.HasSuffix(key, "__revision") {
		return false
	}
	if secretKeyPattern.MatchString(key) {
		return true
	}
	// "execution.*_key" style names: any key that ends in a key suffix is treated
	// as credential-shaped too ("app_key" is a client id, not a secret, but
	// redacting it hides nothing a status reader needs).
	lower := strings.ToLower(key)
	return strings.HasSuffix(lower, "_key") || strings.HasSuffix(lower, ".key")
}

// textRedactionPattern redacts `name=value` / `name: value` pairs whose name looks
// secret-ish, which is how driver errors and health details end up carrying tokens
// ("access_token=abc", "auth: Bearer efg"). It requires the separator, so ordinary
// prose such as "需要重新授权" or "auth status failed" is left alone.
var textRedactionPattern = regexp.MustCompile(`(?i)([a-z0-9_.-]*(?:secret|token|password|passwd|api[_-]?key|apikey|private[_-]?key|credential|authorization)[a-z0-9_.-]*\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)

// bearerPattern redacts `Bearer <credential>` regardless of the key name around it.
var bearerPattern = regexp.MustCompile(`(?i)(\bbearer\s+)[^\s,;]+`)

// redactText scrubs credentials out of a free-form diagnostic string. It is used
// on health details before an inspection view returns them: those strings come
// from drivers' stderr and error chains, which the store cannot vouch for.
func redactText(s string) string {
	if s == "" {
		return s
	}
	s = textRedactionPattern.ReplaceAllString(s, RedactedValue)
	s = bearerPattern.ReplaceAllString(s, "${1}"+RedactedValue)
	return s
}
