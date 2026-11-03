package runtime

// The CLI's read-only view of an install. `zeokeel status` must work while the
// app is running and holds the state directory's write lock, so this file uses
// store.Inspect (mode=ro, no migration, no lock) and never opens the engine. The
// exit codes are the prototype's 006:113-124 contract, kept in one place so the
// CLI, the headless loop and the docs cannot drift.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/zeokeel/super-agent/internal/store"
)

// Exit codes for `zeokeel status` and the headless service (006:113-124).
const (
	ExitOK        = 0 // running and healthy
	ExitRestart   = 1 // not running / unresponsive / runtime error: a restart may fix it
	ExitComponent = 2 // running, but a component reports an error: do not restart
	ExitSetup     = 3 // running, but waiting for setup: do not restart
)

// StatusError is one component that needs attention. Part is stable
// ("schema", "store", "runtime"), so a supervisor script can match it.
type StatusError struct {
	Part   string `json:"part"`
	Detail string `json:"detail"`
}

// StatusHealth is one durable component's self-report, as status prints it.
type StatusHealth struct {
	Part   string    `json:"part"`
	State  string    `json:"state"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at,omitempty"`
}

// StatusLoad is the work the install is carrying right now.
type StatusLoad struct {
	Running    int `json:"running"`
	Queued     int `json:"queued"`
	MaxWorkers int `json:"maxWorkers"`
}

// Status is one install's operator-facing state. Every field is derived from
// durable facts or from the single-instance lock; nothing here guesses.
type Status struct {
	StateDir        string         `json:"stateDir"`
	DBPath          string         `json:"dbPath"`
	Running         bool           `json:"running"`
	ManuallyStopped bool           `json:"manually_stopped"`
	SchemaVersion   int            `json:"schemaVersion"`
	SchemaKnown     bool           `json:"schemaKnown"`
	BuildSchema     int            `json:"buildSchema"`
	Health          []StatusHealth `json:"health"`
	Errors          []StatusError  `json:"errors"`
	Setup           []string       `json:"setup"`
	Load            StatusLoad     `json:"load"`
	OK              bool           `json:"ok"`
}

// ExitCode maps the status to the 006 contract. Component errors win over setup:
// a broken component is the more specific, more urgent answer.
func (s Status) ExitCode() int {
	// A deliberate stop must not be undone by a supervisor: the contract's
	// "manually_stopped=true → do nothing" (ops-runbook §7) wins over "not running
	// = restart". Starting the app by hand clears the marker.
	if s.ManuallyStopped && !s.Running {
		return ExitOK
	}
	if len(s.Errors) > 0 {
		return ExitComponent
	}
	if s.SchemaKnown && s.SchemaVersion > store.SchemaVersion {
		return ExitComponent
	}
	if !s.Running {
		return ExitRestart
	}
	for _, h := range s.Health {
		if h.State == "error" {
			return ExitComponent
		}
	}
	if len(s.Setup) > 0 {
		return ExitSetup
	}
	return ExitOK
}

// OK now. It is the JSON field the runbook documents.
func (s Status) Healthy() bool { return s.ExitCode() == ExitOK }

// ManuallyStoppedKey is the kv marker a deliberate `stop` writes when the app is
// not running. A running app cannot be marked through the kv table (it holds the
// writer lock), so `zeokeel stop` also writes a standalone file, which is what
// status reads in that case; both are cleared by the next start.
const ManuallyStoppedKey = "runtime.manually_stopped"

// manualStopFile is the file marker a running app cannot block.
const manualStopFile = "STOPPED"

// StopMarkerPath is where `zeokeel stop` leaves the "do not restart" fact.
func StopMarkerPath(stateDir string) string { return filepath.Join(stateDir, manualStopFile) }

// MarkManuallyStopped records a deliberate stop. It never needs the database, so
// it works while the app is running (which is exactly when it matters).
func MarkManuallyStopped(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(StopMarkerPath(stateDir), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

// ClearManuallyStopped removes the marker: starting the app is the person saying
// "run again".
func ClearManuallyStopped(stateDir string) error {
	err := os.Remove(StopMarkerPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// InspectStatus reads one state directory without taking the write lock.
//
// Running is detected by trying to acquire the writer lock: when another process
// holds it store.Open returns store.ErrAlreadyRunning. When this process can take
// it, no app is running, and Open is closed immediately. Open may apply
// migrations, which is the one write status can cause; it is what keeps a fresh
// install from looking like a corrupt one.
func InspectStatus(stateDir string) Status {
	st := Status{StateDir: stateDir, BuildSchema: store.SchemaVersion}
	if stateDir == "" {
		st.Errors = append(st.Errors, StatusError{Part: "config", Detail: "没有状态目录"})
		return st
	}
	dbPath := filepath.Join(stateDir, "zeokeel.db")
	st.DBPath = dbPath
	// The deliberate-stop marker is a file, not a kv row: read it before the
	// database check, because a stopped install may not have a database yet (or
	// the app may hold the writer lock).
	if _, err := os.Stat(StopMarkerPath(stateDir)); err == nil {
		st.ManuallyStopped = true
	}
	if _, err := os.Stat(dbPath); err != nil {
		// A fresh install has no database yet: that is setup, not a component
		// error. The process is not running either, so the exit code is 1.
		st.Setup = append(st.Setup, "还没有状态目录数据库：运行 zeokeel setup（或打开应用）完成首次配置")
		return st
	}

	// Probe the single-instance lock. ErrAlreadyRunning means an app is alive.
	if probe, err := store.Open(dbPath); err == nil {
		st.Running = false
		_ = probe.Close()
	} else if errors.Is(err, store.ErrAlreadyRunning) {
		st.Running = true
	} else if errors.Is(err, store.ErrNewerSchema) {
		st.Errors = append(st.Errors, StatusError{Part: "schema", Detail: err.Error()})
	} else {
		st.Errors = append(st.Errors, StatusError{Part: "store", Detail: err.Error()})
	}

	view, err := store.Inspect(dbPath)
	if err != nil {
		st.Errors = append(st.Errors, StatusError{Part: "store", Detail: err.Error()})
		return st
	}
	st.SchemaVersion = view.SchemaVersion
	st.SchemaKnown = view.SchemaVersionKnown
	if !view.SchemaVersionKnown {
		st.Errors = append(st.Errors, StatusError{Part: "schema", Detail: "数据库没有 schema 标记"})
	}
	for _, h := range view.Health {
		st.Health = append(st.Health, StatusHealth{
			Part: h.Entry, State: h.State, Detail: h.Detail, At: h.At,
		})
		if h.State == "error" {
			st.Errors = append(st.Errors, StatusError{Part: h.Entry, Detail: h.Detail})
		}
	}
	if _, err := os.Stat(StopMarkerPath(stateDir)); err == nil {
		st.ManuallyStopped = true
	}
	if view.KV[ManuallyStoppedKey] == "1" {
		st.ManuallyStopped = true
	}

	// Load facts come from Inspect's aggregate counts: the stored stage column is
	// the honest number from the file (the writer's clock), and every waiting
	// effect row is counted by kind.
	st.Load.Running = view.TicketsByStage["working"]
	st.Load.Queued = view.TicketsByStage["working/queued"]
	st.Load.MaxWorkers = settingIntFromKV(view.KV, "setting:execution.concurrency", 2, 1, 8)

	st.Setup = setupGaps(view.KV, view.Workspaces)
	st.OK = st.Healthy()
	return st
}

// setupGaps answers "does this install still need a person?". A marker written by
// the app when it had a model and a workspace is authoritative; otherwise the
// onboarding settings are checked. Empty means configured.
func setupGaps(kv map[string]string, workspaces int) []string {
	if kv["setup.complete"] == "1" || kv["setting:onboard.done"] == "1" {
		return nil
	}
	var gaps []string
	if kv["setting:onboard.model"] == "" {
		gaps = append(gaps, "还没有配置模型")
	}
	if workspaces == 0 {
		gaps = append(gaps, "还没有登记工作区")
	}
	if len(gaps) == 0 {
		return nil
	}
	return gaps
}

// settingIntFromKV reads one numeric setting from the kv map produced by Inspect
// (keys carry the "setting:" namespace). Invalid or out-of-range values fall back
// to def, which is what the runtime uses too.
func settingIntFromKV(kv map[string]string, key string, def, min, max int) int {
	raw, ok := kv[key]
	if !ok {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// SetupHint is the one line the CLI prints when setup is incomplete.
func SetupHint(gaps []string) string {
	if len(gaps) == 0 {
		return ""
	}
	return fmt.Sprintf("等待 setup：%v", gaps)
}
