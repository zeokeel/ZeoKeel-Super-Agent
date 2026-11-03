// Package scan observes a workspace directory and produces the facts a brief is
// made of: what the project is, how to build and test it, and an outline of what
// lives where. It reads; it never writes to the workspace.
//
// The outline matters more than it looks: it is what lets the Router say "this
// request belongs to the data workspace, not the web console" without a model call
// (design §4.6: 简报是缓存不是真相，但缓存要有人更新).
package scan

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// Limits keep a scan cheap: the briefs of ten workspaces are rendered on every
// routing decision, so a scan may not walk a node_modules tree.
const (
	maxDirs      = 8
	maxFilesRead = 4 << 10 // 4KB per README-sized file
)

// Scan reads one workspace root and returns the facts it can observe. It is best
// effort: an unreadable directory yields fewer facts, never an error that would
// stop the agent from starting.
func Scan(root string) domain.Facts {
	facts := domain.Facts{ObservedAt: time.Now()}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return facts
	}
	facts.Remote, facts.DefaultBranch = gitFacts(root)
	facts.DirSummary = dirOutline(root)
	facts.Language, facts.PackageMgr = stackOf(root)
	facts.BuildCmd, facts.TestCmd, facts.LintCmd = commandsFor(facts.Language, facts.PackageMgr)
	facts.AgentsMD = firstHeading(filepath.Join(root, "AGENTS.md"))
	if facts.AgentsMD == "" {
		facts.AgentsMD = firstHeading(filepath.Join(root, "README.md"))
	}
	facts.LastActivity = lastModified(root)
	return facts
}

// gitFacts reads .git/config directly instead of running git: the scan runs at
// startup and must not depend on a working git install or take a subprocess slot.
func gitFacts(root string) (remote, branch string) {
	if b, err := os.ReadFile(filepath.Join(root, ".git", "HEAD")); err == nil {
		line := strings.TrimSpace(string(b))
		branch = strings.TrimPrefix(line, "ref: refs/heads/")
	}
	cfg, err := os.ReadFile(filepath.Join(root, ".git", "config"))
	if err != nil {
		return remote, branch
	}
	inOrigin := false
	for _, line := range strings.Split(string(cfg), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inOrigin = strings.Contains(t, `remote "origin"`)
			continue
		}
		if inOrigin && strings.HasPrefix(t, "url") {
			if _, v, ok := strings.Cut(t, "="); ok {
				remote = strings.TrimSpace(v)
			}
		}
	}
	return remote, branch
}

// dirOutline lists the top-level directories with a hint about each. The hint is
// the directory's dominant extension or its file count, which is usually enough to
// tell "src" from "docs".
func dirOutline(root string) []domain.DirEntry {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []domain.DirEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		if len(out) == maxDirs {
			break
		}
		out = append(out, domain.DirEntry{
			Name:    e.Name(),
			Summary: dirHint(filepath.Join(root, e.Name())),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// dirHint describes a directory by its dominant file type and how many files it
// holds, without descending more than two levels.
func dirHint(dir string) string {
	counts := map[string]int{}
	total := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && path != dir && depth(path, dir) >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		total++
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != "" {
			counts[ext]++
		}
		if total > 500 {
			return filepath.SkipAll
		}
		return nil
	})
	if total == 0 {
		return "空"
	}
	name, best := "", 0
	for ext, n := range counts {
		if n > best || (n == best && ext < name) {
			name, best = ext, n
		}
	}
	if name == "" {
		return strconv.Itoa(total) + " 个文件"
	}
	return strings.TrimPrefix(name, ".") + " · " + strconv.Itoa(total) + " 个文件"
}

func depth(path, base string) int {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return 0
	}
	return len(strings.Split(rel, string(filepath.Separator)))
}

// stackOf names the language and package manager from the files that decide it.
func stackOf(root string) (language, pm string) {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	switch {
	case has("go.mod"):
		return "Go", "go modules"
	case has("package.json"):
		pm = "npm"
		if has("pnpm-lock.yaml") {
			pm = "pnpm"
		} else if has("yarn.lock") {
			pm = "yarn"
		}
		if has("tsconfig.json") {
			return "TypeScript", pm
		}
		return "JavaScript", pm
	case has("pyproject.toml"), has("requirements.txt"):
		return "Python", "pip/uv"
	case has("Cargo.toml"):
		return "Rust", "cargo"
	}
	return "", ""
}

// commandsFor are the commands a Worker should try first.
func commandsFor(language, pm string) (build, test, lint string) {
	switch language {
	case "Go":
		return "go build ./...", "go test ./...", "go vet ./..."
	case "TypeScript", "JavaScript":
		switch pm {
		case "pnpm":
			return "pnpm build", "pnpm test", "pnpm lint"
		case "yarn":
			return "yarn build", "yarn test", "yarn lint"
		}
		return "npm run build", "npm test", "npm run lint"
	case "Python":
		return "", "pytest", "ruff check ."
	case "Rust":
		return "cargo build", "cargo test", "cargo clippy"
	}
	return "", "", ""
}

// firstHeading reads the first markdown heading, as a one-line hint of the project.
func firstHeading(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, maxFilesRead)
	n, _ := f.Read(buf)
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			return strings.TrimSpace(strings.TrimLeft(t, "#"))
		}
	}
	return ""
}

// lastModified is the newest mtime a few levels deep, used by the digest's "最近活动".
func lastModified(root string) time.Time {
	var newest time.Time
	count := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && depth(path, root) >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		count++
		if count > 2000 {
			return filepath.SkipAll
		}
		return nil
	})
	return newest
}
