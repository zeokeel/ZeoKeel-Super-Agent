package main

// IM wiring: the runtime owns the per-channel lifecycle; this file only turns
// command-line flags into the runtime's flag overlay, migrates a flag-only
// deployment to an explicit enabled state (P0-4), and starts/stops the manager at
// boot. A build with no credentials must not pretend to listen.

import (
	"context"
	"errors"
	"log/slog"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/runtime"

	"github.com/zeokeel/super-agent/internal/im/dingtalk"
)

// imOptions is the deployment's IM configuration from flags. Saved settings are
// read by the runtime (they are the fallback); flags win for this run and are
// reapplied on every settings save by the manager.
type imOptions struct {
	FeishuAppID     string
	FeishuAppSecret string
	FeishuChats     string
	DingAppKey      string
	DingAppSecret   string
	DingRobot       string
	SelfID          string
}

// flags converts the command-line options to the runtime's overlay type.
func (o imOptions) flags() runtime.IMFlags {
	return runtime.IMFlags{
		FeishuAppID: o.FeishuAppID, FeishuAppSecret: o.FeishuAppSecret,
		FeishuChats: o.FeishuChats, DingAppKey: o.DingAppKey,
		DingAppSecret: o.DingAppSecret, DingRobot: o.DingRobot, SelfID: o.SelfID,
	}
}

// permanentAwareSender keeps a permanent DWS/IM failure (dws missing, 401/403,
// bad parameters) from being retried by the outbox forever (P1-11): outbox treats
// OutcomeGone as "never again", and the channel's health row shows the action.
// context.Canceled is deliberately not permanent: a shutdown must not discard a
// receipt the next start can still deliver.
type permanentAwareSender struct{ inner im.Sender }

func (p permanentAwareSender) Send(ctx context.Context, msg im.Outbound) (im.Receipt, error) {
	receipt, err := p.inner.Send(ctx, msg)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return im.Receipt{}, err // retry after restart
		}
		if dingtalk.IsPermanent(err) ||
			errors.Is(err, dingtalk.ErrDWSAuthRequired) ||
			errors.Is(err, dingtalk.ErrAuthRequired) {
			return im.Receipt{Gone: true}, nil
		}
	}
	return receipt, err
}

func (p permanentAwareSender) ReadThread(ctx context.Context, conv domain.Conversation, cursor string) (im.Page, error) {
	return p.inner.ReadThread(ctx, conv, cursor)
}

// disabledIMSender settles outbox messages for a channel that is no longer
// listening: they must not be sent through a stale driver, and must not retry
// forever. The panel's 来源 row already says 未启用.
type disabledIMSender struct{}

func (disabledIMSender) Send(context.Context, im.Outbound) (im.Receipt, error) {
	return im.Receipt{Gone: true}, nil
}
func (disabledIMSender) ReadThread(context.Context, domain.Conversation, string) (im.Page, error) {
	return im.Page{Done: true}, nil
}

// startConfiguredIM attaches the channel manager and applies the boot
// configuration (settings + flags). Saved-source changes are applied by the
// runtime's SaveSettings hook from then on. The returned manager is stopped by
// main's defer so shutdown closes drivers and withdraws senders (P1-10).
func startConfiguredIM(ctx context.Context, rt *runtime.Runtime, sender *sourceSender, o imOptions, logger *slog.Logger) *runtime.IMManager {
	if logger == nil {
		logger = slog.Default()
	}
	flags := o.flags()
	// P0-4: a deployment configured only on the command line becomes explicitly
	// enabled in settings (persisted) before the manager reads the config.
	rt.BootstrapIMFlags(flags)
	opts := runtime.IMManagerOptions{Logger: logger, Flags: flags}
	if sender != nil {
		opts.RegisterSender = func(kind string, s im.Sender) {
			sender.setIM(kind, permanentAwareSender{inner: s})
		}
		opts.UnregisterSender = func(kind string) {
			sender.setIM(kind, disabledIMSender{})
		}
	}
	m := runtime.NewIMManager(rt, opts)
	rt.AttachIM(m)
	m.Start(ctx, rt.IMConfig().WithFlags(flags))
	return m
}
