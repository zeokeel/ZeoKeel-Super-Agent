package dingtalk

// DWS setup probes for the panel's guided connect flow (UX review U-03/A18):
// install/version detection and the exact login command a person must run. The
// panel never starts an interactive login itself; it reports the command/URL and
// re-checks after the person says they are done.

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"strings"
)

// DWSVersion runs `dws version` (then `dws --version` for older CLIs) and returns
// the first non-empty line. A missing binary is reported as a permanent error so
// the caller can show "install dws" instead of a generic connection failure.
func (c *DWSChannel) DWSVersion(ctx context.Context) (string, error) {
	if err := c.ensureOpen(); err != nil {
		return "", err
	}
	var lastErr error
	for _, args := range [][]string{{"version"}, {"--version"}} {
		stdout, stderr, err := c.runCommand(ctx, args...)
		if err != nil {
			lastErr = c.classifyCmdError("version", err, stdout, stderr)
			if IsMissingBinary(lastErr) {
				return "", lastErr
			}
			continue
		}
		for _, line := range strings.Split(string(stdout), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if len(line) > 120 {
				line = line[:120] + "…"
			}
			return line, nil
		}
		// Exit 0 with empty stdout is a valid "version unknown" answer: the CLI is
		// installed, which is the fact the panel needs.
		return "unknown", nil
	}
	if lastErr == nil {
		lastErr = &DWSError{Op: "version", Detail: "dws version 没有返回可读输出"}
	}
	return "", lastErr
}

// LoginCommand returns the argv a person runs in a terminal. It derives from the
// configured command/profile/headless mode and is never executed by the panel.
func (c *DWSChannel) LoginCommand() []string {
	cmd := strings.TrimSpace(c.cfg.Command)
	if cmd == "" {
		cmd = "dws"
	}
	args := []string{cmd, "auth", "login"}
	if c.cfg.LoginHeadless {
		args = append(args, "--device")
	}
	if profile := strings.TrimSpace(c.cfg.Profile); profile != "" {
		args = append(args, "--profile", profile)
	}
	return args
}

// Profile returns the configured DWS profile ("" means the CLI's current one).
func (c *DWSChannel) Profile() string { return strings.TrimSpace(c.cfg.Profile) }

// IsMissingBinary reports whether err means the dws executable is absent or not
// runnable (the "install dws" action, not a transient network error).
func IsMissingBinary(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return true
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	var de *DWSError
	if errors.As(err, &de) {
		low := strings.ToLower(de.Detail)
		for _, marker := range []string{"未安装或不可执行", "not executable", "executable file not found", "no such file or directory"} {
			if strings.Contains(low, marker) {
				return true
			}
		}
	}
	return false
}
