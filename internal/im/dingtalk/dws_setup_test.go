package dingtalk

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// setupFakeCLI scripts the setup probes: version/auth output, and a mode where the
// binary is missing.
type setupFakeCLI struct {
	mu      sync.Mutex
	runs    [][]string
	version string
	missing bool
	failVer bool
}

func (c *setupFakeCLI) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	c.mu.Lock()
	c.runs = append(c.runs, append([]string(nil), args...))
	c.mu.Unlock()
	if c.missing {
		return nil, nil, &exec.Error{Name: "dws", Err: exec.ErrNotFound}
	}
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "version"):
		if c.failVer && joined == "version" {
			return nil, nil, errors.New("unknown version command")
		}
		return []byte(c.version + "\n"), nil, nil
	default:
		return nil, nil, errors.New("setupFakeCLI: unexpected " + joined)
	}
}

func (c *setupFakeCLI) StartStream(context.Context, ...string) (DWSStream, error) {
	return nil, errors.New("not used")
}

func (c *setupFakeCLI) commands() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.runs))
	for _, run := range c.runs {
		out = append(out, strings.Join(run, " "))
	}
	return out
}

func newSetupChannel(t *testing.T, cli DWSCLI, cfg DWSConfig) *DWSChannel {
	t.Helper()
	cfg.Client = cli
	ch, err := NewDWSChannel(cfg, cli)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	return ch
}

func TestDWSVersion(t *testing.T) {
	cli := &setupFakeCLI{version: "dws v0.9.1 (abc123)"}
	ch := newSetupChannel(t, cli, DWSConfig{Command: "dws"})
	got, err := ch.DWSVersion(context.Background())
	if err != nil || !strings.Contains(got, "0.9.1") {
		t.Fatalf("DWSVersion = %q/%v", got, err)
	}
	if got := cli.commands(); len(got) == 0 || got[0] != "version" {
		t.Fatalf("ran %v, want `version` first", got)
	}
}

func TestDWSVersionFallsBackToFlag(t *testing.T) {
	cli := &setupFakeCLI{version: "dws v0.8.0", failVer: true}
	ch := newSetupChannel(t, cli, DWSConfig{Command: "dws"})
	got, err := ch.DWSVersion(context.Background())
	if err != nil || !strings.Contains(got, "0.8.0") {
		t.Fatalf("fallback DWSVersion = %q/%v", got, err)
	}
	commands := cli.commands()
	if len(commands) < 2 || commands[0] != "version" || commands[1] != "--version" {
		t.Fatalf("runs = %v, want version then --version", commands)
	}
}

func TestDWSVersionMissingBinary(t *testing.T) {
	cli := &setupFakeCLI{missing: true}
	ch := newSetupChannel(t, cli, DWSConfig{Command: "dws"})
	_, err := ch.DWSVersion(context.Background())
	if err == nil || !IsMissingBinary(err) {
		t.Fatalf("DWSVersion missing = %v (want IsMissingBinary)", err)
	}
	// A plain network-ish error must not be classified as "install dws".
	if IsMissingBinary(errors.New("connection refused")) {
		t.Fatal("a transient error was classified as a missing binary")
	}
}

func TestDWSLoginCommand(t *testing.T) {
	ch := newSetupChannel(t, &setupFakeCLI{}, DWSConfig{Command: "/opt/dws", Profile: "work"})
	got := ch.LoginCommand()
	want := []string{"/opt/dws", "auth", "login", "--profile", "work"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("LoginCommand = %v, want %v", got, want)
	}
	headless := newSetupChannel(t, &setupFakeCLI{}, DWSConfig{Command: "dws", LoginHeadless: true})
	if got := strings.Join(headless.LoginCommand(), " "); got != "dws auth login --device" {
		t.Fatalf("headless LoginCommand = %q", got)
	}
}
