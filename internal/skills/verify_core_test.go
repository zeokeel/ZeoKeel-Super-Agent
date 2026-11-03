package skills_test

// verifier-A: independent falsification of the Wave-1 skills claim (audit N-6:
// path containment via os.Root). The tests below attack the boundary from the
// outside: a symlinked SKILL.md and a symlinked skill directory that point out of
// the skills root. A pass means Load refused, not silently skipped.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/super-agent/internal/skills"
)

func skillMD(name, body string) string {
	return "---\nname: " + name + "\ndescription: test skill\nwhen-to-use: test\n---\n\n" + body + "\n"
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// A SKILL.md symlink pointing outside the root must be refused, and its content
// must never reach the prompt (the package exists to stop prompt injection).
func TestVerifySkillFileSymlinkEscapeRefused(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "SKILL.md")
	writeFile(t, secret, skillMD("leak", "SECRET-CONTENT-MUST-NOT-BE-LOADED"))

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "leak"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "leak", "SKILL.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	set, err := skills.Load(root, skills.Options{})
	if err == nil {
		t.Fatalf("a SKILL.md symlink escaping the root was accepted; loaded %v", set.Names())
	}
	set, err = skills.Load(root, skills.Options{})
	if set != nil {
		for _, name := range set.Names() {
			if sk, ok := set.Get(name); ok {
				if sk.Name == "leak" {
					t.Fatalf("the escaping skill %q was loaded from %q", sk.Name, sk.Source)
				}
			}
		}
	}
}

// A directory symlink pointing outside the root must be refused too.
func TestVerifySkillDirSymlinkEscapeRefused(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "evil", "SKILL.md"), skillMD("evil", "outside body"))

	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if set, err := skills.Load(root, skills.Options{}); err == nil {
		t.Fatalf("a directory symlink escaping the root was accepted; loaded %v", set.Names())
	}
}

// An ABSOLUTE symlink that resolves INSIDE the root keeps working (the old
// behavior the handoff promises to preserve): the body is real, the boundary is
// still os.Root.
func TestVerifySkillAbsoluteSymlinkInsideRootAllowed(t *testing.T) {
	root := t.TempDir()
	// The real content lives in a underscore directory: the loader skips such
	// directories as skill entries, so only the alias is a skill.
	writeFile(t, filepath.Join(root, "_support", "real-skill.md"), skillMD("alias", "inside-the-root"))
	aliasDir := filepath.Join(root, "alias")
	if err := os.MkdirAll(aliasDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "_support", "real-skill.md"), filepath.Join(aliasDir, "SKILL.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	set, err := skills.Load(root, skills.Options{})
	if err != nil {
		t.Fatalf("an absolute symlink resolving inside the root was refused: %v", err)
	}
	sk, ok := set.Get("alias")
	if !ok {
		t.Fatalf("skill alias missing; got %v", set.Names())
	}
	if sk.Body == "" {
		t.Fatalf("skill body was not read through the symlink: %+v", sk)
	}
}
