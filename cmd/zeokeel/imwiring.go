package main

// IM wiring: the runtime owns the per-channel lifecycle; this file only turns
// command-line flags into the runtime's flag overlay and starts the manager at
// boot. A build with no credentials must not pretend to listen.

import (
	"context"
	"log/slog"

	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/runtime"
)

// imOptions is the deployment's IM configuration from flags. Saved settings are
// read by the runtime (they are the fallback); flags win for this run.
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

// startConfiguredIM attaches the channel manager and applies the boot
// configuration (settings + flags). Saved-source changes are applied by the
// runtime's SaveSettings hook from then on.
func startConfiguredIM(ctx context.Context, rt *runtime.Runtime, sender *sourceSender, o imOptions, logger *slog.Logger) *runtime.IMManager {
	if logger == nil {
		logger = slog.Default()
	}
	opts := runtime.IMManagerOptions{Logger: logger}
	if sender != nil {
		opts.RegisterSender = func(kind string, s im.Sender) { sender.setIM(kind, s) }
	}
	m := runtime.NewIMManager(rt, opts)
	rt.AttachIM(m)
	m.Start(ctx, rt.IMConfig().WithFlags(o.flags()))
	return m
}
