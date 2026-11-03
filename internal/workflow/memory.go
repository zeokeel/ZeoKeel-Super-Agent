package workflow

import (
	"context"
	"sync"
)

// MemoryStore is the in-process store used by the CLI's -pipeline mode; a run is
// lost when the process exits (documented in the package comment).
type MemoryStore struct {
	mu   sync.Mutex
	runs map[string]*Run
}

// NewMemoryStore builds an empty store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{runs: map[string]*Run{}} }

// SaveRun implements Store.
func (m *MemoryStore) SaveRun(_ context.Context, r *Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *r
	cp.Input, cp.Outputs, cp.StepStatus = clone(r.Input), clone(r.Outputs), clone(r.StepStatus)
	m.runs[r.ID] = &cp
	return nil
}

// LoadRun implements Store.
func (m *MemoryStore) LoadRun(_ context.Context, id string) (*Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	cp.Input, cp.Outputs, cp.StepStatus = clone(r.Input), clone(r.Outputs), clone(r.StepStatus)
	return &cp, nil
}

var _ Store = (*MemoryStore)(nil)
