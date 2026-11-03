package runtime

// Durable small values the runtime owns: Source resume cursors and the setup
// marker the CLI reads without opening the engine. They live in the store's kv
// table (the same table settings and the schema stamp use), so a restart resumes a
// Source instead of replaying from "now" (design §7.2).
//
// The strings are opaque to the store: a GitHub cursor is the driver's marker, a
// schedule cursor is a fire id, an IM cursor is a page token.

const (
	// cursorPrefix namespaces a driver's resume token by Source instance. The
	// instance part is the same name the panel shows ("github:<owner/name>").
	cursorPrefix = "source.cursor."
	// setupCompleteKey is written when the install has both a model and at least
	// one workspace, so `zeokeel status` can say "setup done" without a writer.
	setupCompleteKey = "setup.complete"
)

// Cursor returns the resume token saved for one Source instance, or "". A failure
// to read is treated as "no cursor": replaying a Source is idempotent (content
// derived Trigger ids dedupe), while failing to start it is not.
func (r *Runtime) Cursor(source string) string {
	r.mu.Lock()
	mem := r.cursors[source]
	st := r.settingsStore
	r.mu.Unlock()
	if st != nil {
		if v, ok, err := st.Get(cursorPrefix + source); err == nil && ok {
			return v
		}
	}
	return mem
}

// SetCursor persists a Source's resume token. The in-memory copy is kept even
// when no store is attached, so a memory-mode demo still advances.
func (r *Runtime) SetCursor(source, value string) error {
	if source == "" || value == "" {
		return nil
	}
	r.mu.Lock()
	if r.cursors == nil {
		r.cursors = map[string]string{}
	}
	r.cursors[source] = value
	st := r.settingsStore
	r.mu.Unlock()
	if st == nil {
		return nil
	}
	return st.Set(cursorPrefix+source, value)
}

// MarkSetupComplete records that this install has a model and a workspace. The
// marker is what makes `zeokeel status` exit 0/3 honest: status must not open the
// engine, so it cannot ask the engine whether it could run.
func (r *Runtime) MarkSetupComplete() error {
	r.mu.Lock()
	r.setupMarked = true
	st := r.settingsStore
	r.mu.Unlock()
	if st == nil {
		return nil
	}
	return st.Set(setupCompleteKey, "1")
}

// SetupComplete reports whether this process has already recorded a configured
// install. It reads memory first (the app's own view) and then the kv table.
func (r *Runtime) SetupComplete() bool {
	r.mu.Lock()
	marked := r.setupMarked
	st := r.settingsStore
	r.mu.Unlock()
	if marked {
		return true
	}
	if st != nil {
		if saved, ok, err := st.Get(setupCompleteKey); err == nil && ok && saved == "1" {
			return true
		}
	}
	return false
}
