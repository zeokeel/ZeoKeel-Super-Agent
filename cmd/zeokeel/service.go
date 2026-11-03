package main

// Headless service mode: the same core the window runs, without mygo. The loop is
// the prototype's "2s pass" plus the in-process supervisor; SIGINT/SIGTERM stops
// it cleanly and the process exits with the 006 status code (0/1/2/3).

import (
	"context"
	"log/slog"
	"time"

	"github.com/zeokeel/super-agent/internal/runtime"
)

// servicePassInterval is the prototype's tick: every 2 seconds the runtime
// advances the Eval queue and republishes the projections.
const servicePassInterval = 2 * time.Second

// serveHeadless runs until ctx is canceled, then drains and returns the exit code.
func serveHeadless(ctx context.Context, rt *runtime.Runtime, logger *slog.Logger) int {
	if logger == nil {
		logger = slog.Default()
	}
	rt.StartSupervisor(ctx, time.Minute, logger)
	logger.Info("headless service running", "pass", servicePassInterval.String())

	t := time.NewTicker(servicePassInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("shutdown signal received; draining executors")
			rt.Publish()
			if !rt.WaitIdle(10 * time.Second) {
				logger.Warn("executors still running after the 10s drain window")
			}
			state, detail, code := rt.Health()
			logger.Info("headless service stopped", "state", state, "detail", detail, "exitCode", code)
			return code
		case <-t.C:
			if _, err := rt.EvaluateDue(time.Now()); err != nil {
				logger.Error("eval pass failed", "error", err)
			}
			rt.Publish()
		}
	}
}
