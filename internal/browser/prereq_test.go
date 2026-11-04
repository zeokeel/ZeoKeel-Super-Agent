package browser

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePrereqExec fakes LookPath + `<tool> --version` (no process, no network).
type fakePrereqExec struct {
	mu       sync.Mutex
	present  map[string]bool
	versions map[string]string
	fail     map[string]error
	block    map[string]bool
	calls    [][]string
	lookups  []string
}

func (f *fakePrereqExec) lookPath(name string) (string, error) {
	f.mu.Lock()
	f.lookups = append(f.lookups, name)
	present := f.present[name]
	f.mu.Unlock()
	if !present {
		return "", exec.ErrNotFound
	}
	return "/opt/fake/bin/" + name, nil
}

func (f *fakePrereqExec) run(ctx context.Context, name string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	block := f.block[name]
	err := f.fail[name]
	version := f.versions[name]
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if err != nil {
		return "some warning\n" + err.Error(), err
	}
	return version + "\n", nil
}

func newPrereqManager(t *testing.T, fake *fakePrereqExec, tweak func(*Options)) *Manager {
	t.Helper()
	opts := Options{
		LookPath:   fake.lookPath,
		RunCommand: fake.run,
		Detector:   fakeDetector,
		Launcher:   &fakeLauncher{},
		Prober:     &fakeProber{},
	}
	if tweak != nil {
		tweak(&opts)
	}
	m, err := NewManager(t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestPrerequisitesAllPresent(t *testing.T) {
	fake := &fakePrereqExec{
		present:  map[string]bool{"node": true, "npx": true},
		versions: map[string]string{"node": "v20.11.1", "npx": "10.2.4"},
	}
	m := newPrereqManager(t, fake, nil)
	st := m.Prerequisites(context.Background())
	if !st.NodeFound || !st.NPXFound || st.NodeVersion != "v20.11.1" || st.NPXVersion != "10.2.4" {
		t.Fatalf("status = %+v", st)
	}
	if st.NodePath != "/opt/fake/bin/node" || st.NPXPath != "/opt/fake/bin/npx" {
		t.Fatalf("paths = %+v", st)
	}
	if len(st.InstallHints) != 0 {
		t.Fatalf("installed prerequisites still produced hints: %v", st.InstallHints)
	}
	if st.InstallURL != nodeInstallURL {
		t.Fatalf("install URL = %q", st.InstallURL)
	}
}

func TestPrerequisitesNodeMissing(t *testing.T) {
	fake := &fakePrereqExec{
		present:  map[string]bool{"npx": true},
		versions: map[string]string{"npx": "10.2.4"},
	}
	st := newPrereqManager(t, fake, nil).Prerequisites(context.Background())
	if st.NodeFound || !st.NPXFound {
		t.Fatalf("status = %+v", st)
	}
	joined := strings.Join(st.InstallHints, " ")
	if !strings.Contains(joined, "Node.js") || !strings.Contains(joined, "node") {
		t.Fatalf("node-missing hints = %v", st.InstallHints)
	}
	if !strings.Contains(joined, nodeInstallURL) {
		t.Fatalf("node-missing hints lack install URL: %v", st.InstallHints)
	}
}

func TestPrerequisitesNPXMissing(t *testing.T) {
	fake := &fakePrereqExec{
		present:  map[string]bool{"node": true},
		versions: map[string]string{"node": "v20.11.1"},
	}
	st := newPrereqManager(t, fake, nil).Prerequisites(context.Background())
	if !st.NodeFound || st.NPXFound {
		t.Fatalf("status = %+v", st)
	}
	joined := strings.Join(st.InstallHints, " ")
	if !strings.Contains(joined, "npx") || !strings.Contains(joined, "Node.js") {
		t.Fatalf("npx-missing hints = %v", st.InstallHints)
	}
}

func TestPrerequisitesBothMissing(t *testing.T) {
	st := newPrereqManager(t, &fakePrereqExec{present: map[string]bool{}}, nil).Prerequisites(context.Background())
	if st.NodeFound || st.NPXFound || len(st.InstallHints) == 0 {
		t.Fatalf("status = %+v", st)
	}
	joined := strings.Join(st.InstallHints, " ")
	if !strings.Contains(joined, "Node.js") || !strings.Contains(joined, "npx") || !strings.Contains(joined, "重启") {
		t.Fatalf("both-missing hints = %v", st.InstallHints)
	}
}

func TestPrerequisitesCommandTimeout(t *testing.T) {
	fake := &fakePrereqExec{
		present:  map[string]bool{"node": true, "npx": true},
		versions: map[string]string{"npx": "10.2.4"},
		block:    map[string]bool{"node": true},
	}
	m := newPrereqManager(t, fake, func(o *Options) { o.PrereqTimeout = 20 * time.Millisecond })
	start := time.Now()
	st := m.Prerequisites(context.Background())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("prerequisite probe blocked for %s", elapsed)
	}
	if !st.NodeFound || st.NodeVersion != "" {
		t.Fatalf("node timeout status = %+v", st)
	}
	joined := strings.Join(st.InstallHints, " ")
	if !strings.Contains(joined, "node --version") || !strings.Contains(joined, "超时") {
		t.Fatalf("timeout hints = %v", st.InstallHints)
	}
}

func TestPrerequisitesVersionCommandFailure(t *testing.T) {
	fake := &fakePrereqExec{
		present:  map[string]bool{"node": true, "npx": true},
		versions: map[string]string{"npx": "10.2.4"},
		fail:     map[string]error{"node": errors.New("permission denied")},
	}
	st := newPrereqManager(t, fake, nil).Prerequisites(context.Background())
	if !st.NodeFound || st.NodeVersion != "" {
		t.Fatalf("status = %+v", st)
	}
	if !strings.Contains(strings.Join(st.InstallHints, " "), "node --version") {
		t.Fatalf("failure hints = %v", st.InstallHints)
	}
}

// The probe runs a fixed argv without a shell: no user-controlled text, no
// metacharacters, one `--version` argument.
func TestPrerequisitesInvocationIsShellFree(t *testing.T) {
	fake := &fakePrereqExec{
		present:  map[string]bool{"node": true, "npx": true},
		versions: map[string]string{"node": "v20.11.1", "npx": "10.2.4"},
	}
	_ = newPrereqManager(t, fake, nil).Prerequisites(context.Background())
	fake.mu.Lock()
	calls := append([][]string(nil), fake.calls...)
	fake.mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("calls = %v", calls)
	}
	for _, call := range calls {
		if call[0] != "node" && call[0] != "npx" {
			t.Fatalf("unexpected executable %q", call[0])
		}
		if len(call) != 2 || call[1] != "--version" {
			t.Fatalf("argv = %v, want <tool> --version", call)
		}
		for _, arg := range call {
			if strings.ContainsAny(arg, "|&;<>$`\n") {
				t.Fatalf("argv contains shell metacharacters: %v", call)
			}
		}
	}
}

func TestStartFailsEarlyWhenNodeMissing(t *testing.T) {
	fake := &fakePrereqExec{present: map[string]bool{"npx": true}, versions: map[string]string{"npx": "10.2.4"}}
	launcher := &fakeLauncher{}
	env := newTestEnv(t, func(o *Options) {
		o.MCPStarter = nil // production starter -> node/npx are a hard prerequisite
		o.LookPath = fake.lookPath
		o.RunCommand = fake.run
		o.Launcher = launcher
	})
	_, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: true})
	if !errors.Is(err, ErrNodeMissing) {
		t.Fatalf("Start error = %v", err)
	}
	if launcher.count() != 0 {
		t.Fatal("browser was launched although node is missing")
	}
	status := env.m.Status("ws_web")
	if status.State != StateError || status.MCPState != "error" {
		t.Fatalf("status = %+v", status)
	}
	for _, want := range []string{"Node.js", "npx", nodeInstallURL} {
		if !strings.Contains(status.MCPDetail, want) {
			t.Fatalf("MCPDetail %q misses %q", status.MCPDetail, want)
		}
	}
	if !strings.Contains(status.Detail, "Node.js") {
		t.Fatalf("Status.Detail is not actionable: %q", status.Detail)
	}
}

func TestStartFailsEarlyWhenNPXMissing(t *testing.T) {
	fake := &fakePrereqExec{present: map[string]bool{"node": true}, versions: map[string]string{"node": "v20.11.1"}}
	env := newTestEnv(t, func(o *Options) {
		o.MCPStarter = nil
		o.LookPath = fake.lookPath
		o.RunCommand = fake.run
	})
	_, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: true})
	if !errors.Is(err, ErrNodeMissing) {
		t.Fatalf("Start error = %v", err)
	}
	if !strings.Contains(env.m.Status("ws_web").MCPDetail, "npx") {
		t.Fatalf("MCPDetail = %q", env.m.Status("ws_web").MCPDetail)
	}
}
