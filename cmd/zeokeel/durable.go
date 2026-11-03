package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zeokeel/super-agent/internal/effects"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/outbox"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/store"
)

// durable is the on-disk state the desktop app keeps between runs.
type durable struct {
	store *store.Store
	queue *effects.Queue
	box   *outbox.Outbox
	pers  *engine.Persistence
}

// openDurable opens the state directory and wires the durable path: everything a
// command writes commits to SQLite, and starting a Ticket is a queue row rather
// than a goroutine, so a crash resumes instead of losing work (design §7).
func openDurable(eng *engine.Engine, stateDir string, sender outbox.Sender) (*durable, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("state directory: %w", err)
	}
	st, err := store.Open(filepath.Join(stateDir, "zeokeel.db"))
	if err != nil {
		return nil, err
	}
	return openDurableOn(eng, st, sender)
}

// openDurableOn wires the durable path onto a store that is already open. The
// command opens it early so saved settings can configure the model before the
// engine - and its Eval - are built.
func openDurableOn(eng *engine.Engine, st *store.Store, sender outbox.Sender) (*durable, error) {
	q, err := effects.New(st)
	if err != nil {
		st.Close()
		return nil, err
	}
	if sender == nil {
		sender = panelSender{}
	}
	box, err := outbox.Open(st, q, sender)
	if err != nil {
		st.Close()
		return nil, err
	}
	pers, err := engine.NewPersistence(eng, st, q, box)
	if err != nil {
		st.Close()
		return nil, err
	}
	if err := pers.Load(); err != nil {
		st.Close()
		return nil, err
	}
	return &durable{store: st, queue: q, box: box, pers: pers}, nil
}

// Close stops the queue and closes the database.
func (d *durable) Close() {
	if d == nil {
		return
	}
	if d.queue != nil {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = d.queue.Drain(ctx)
	}
	if d.store != nil {
		_ = d.store.Close()
	}
}

// panelSender records replies that have no external channel yet.
type panelSender struct{}

// Send implements outbox.Sender.
func (panelSender) Send(context.Context, outbox.Message) (outbox.SendOutcome, error) {
	return outbox.OutcomeSent, nil
}

// attachDurable connects a runtime to the durable state.
func attachDurable(rt *runtime.Runtime, d *durable) error {
	if d == nil {
		return nil
	}
	rt.SetOutbox(d.box)
	if err := rt.LoadSettings(d.store); err != nil {
		// Preferences are not worth failing the app over; the page says "内存".
		fmt.Fprintln(os.Stderr, "zeokeel: 设置读取失败，本次以默认值运行:", err)
	}
	if err := rt.EnableDurable(d.pers, d.queue); err != nil {
		return err
	}
	// The queue only runs once started, and the launch handler is registered by
	// EnableDurable, so this is the first moment it can safely begin (design §7.2).
	d.queue.Start()
	return nil
}

// defaultStateDir is where the app keeps its database when no flag says otherwise.
func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".zeokeel"
	}
	return filepath.Join(home, ".zeokeel")
}
