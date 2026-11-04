package browser

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultPrereqTimeout = 3 * time.Second
	nodeInstallURL       = "https://nodejs.org/en/download"
)

// runVersionCommand is the production prerequisite probe: a fixed argv
// (`<tool> --version`) executed without a shell, bounded by PrereqTimeout.
func runVersionCommand(ctx context.Context, name string, args ...string) (string, error) {
	// Prerequisites supplies the timeout; keep this helper defensive too.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultPrereqTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Prerequisites checks the local Node.js/npx prerequisite. It never installs
// anything and never touches the network: it only looks the binaries up and runs
// `<tool> --version` with a timeout, returning actionable install hints.
func (m *Manager) Prerequisites(ctx context.Context) PrereqStatus {
	if ctx == nil {
		ctx = context.Background()
	}
	st := PrereqStatus{InstallURL: nodeInstallURL, CheckedAt: m.opts.Now()}
	type toolCheck struct {
		name    string
		found   *bool
		version *string
		path    *string
	}
	checks := []toolCheck{
		{"node", &st.NodeFound, &st.NodeVersion, &st.NodePath},
		{"npx", &st.NPXFound, &st.NPXVersion, &st.NPXPath},
	}
	for _, check := range checks {
		path, err := m.opts.LookPath(check.name)
		if err != nil {
			continue // missing; hints are derived after the loop
		}
		*check.found = true
		*check.path = path
		probeCtx, cancel := context.WithTimeout(ctx, m.opts.PrereqTimeout)
		out, runErr := m.opts.RunCommand(probeCtx, check.name, "--version")
		cancel()
		if runErr != nil {
			continue // found but not runnable; hint below says so
		}
		*check.version = firstLine(out)
	}
	st.InstallHints = prereqHints(st)
	return st
}

// prereqHints is the ordered, actionable list the panel/customer reads. The
// Node.js URL is offered as text only (the package performs no network I/O).
func prereqHints(st PrereqStatus) []string {
	var hints []string
	add := func(s string) {
		for _, h := range hints {
			if h == s {
				return
			}
		}
		hints = append(hints, s)
	}
	switch {
	case !st.NodeFound && !st.NPXFound:
		add("安装 Node.js LTS（自带 npm 与 npx）：" + nodeInstallURL)
		add("安装后完全退出并重启本应用，让新的 PATH 生效")
	case !st.NodeFound:
		add("检测到 npx 但没有 node：确认 PATH 指向同一个 Node.js 安装")
		add("或重新安装官方 Node.js（含 node/npm/npx）：" + nodeInstallURL)
	case !st.NPXFound:
		add("检测到 Node.js 但没有 npx：重新安装官方 Node.js（npx 随附 npm）")
		add("检查 PATH/npm 全局目录；macOS/Linux 也可用 nvm 安装")
	}
	if st.NodeFound && st.NodeVersion == "" {
		add("已检测到 node，但运行 `node --version` 失败（超时/权限/损坏）：请在终端手动确认")
	}
	if st.NPXFound && st.NPXVersion == "" {
		add("已检测到 npx，但运行 `npx --version` 失败（超时/权限/损坏）：请在终端手动确认")
	}
	if st.NodeFound && st.NPXFound && st.NodeVersion != "" && st.NPXVersion != "" {
		return nil // nothing to install or repair
	}
	return hints
}

// prereqFailureDetail is one sentence assembled for Status.MCPDetail and the
// Start error; it always names Node.js and npx (the customer-facing terms).
func prereqFailureDetail(st PrereqStatus) string {
	var missing []string
	if !st.NodeFound {
		missing = append(missing, "node")
	} else if st.NodeVersion == "" {
		missing = append(missing, "node --version")
	}
	if !st.NPXFound {
		missing = append(missing, "npx")
	} else if st.NPXVersion == "" {
		missing = append(missing, "npx --version")
	}
	detail := "缺少 chrome-devtools MCP 的运行前提："
	if len(missing) > 0 {
		detail += strings.Join(missing, "、") + " 不可用。"
	}
	detail += "请安装 Node.js（含 npx），安装后重启应用再检查。"
	if st.InstallURL != "" {
		detail += " 下载：" + st.InstallURL
	}
	if len(st.InstallHints) > 0 {
		detail += " 建议：" + st.InstallHints[0]
	}
	return detail
}

// firstLine returns the first non-empty trimmed line (version output may include
// warnings before the actual version).
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > 80 {
				line = line[:80] + "…"
			}
			return line
		}
	}
	return ""
}

// prereqReady reports whether Start may proceed to launch the browser + MCP.
func (st PrereqStatus) prereqReady() bool {
	return st.NodeFound && st.NPXFound && st.NodeVersion != "" && st.NPXVersion != ""
}
