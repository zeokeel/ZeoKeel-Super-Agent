package brief

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

func testWorkspace() domain.Workspace {
	return domain.Workspace{
		ID: "ws_web", Name: "webapp", Root: "/tmp/webapp", Kind: domain.KindRepo,
		Brief: domain.Brief{
			Purpose: "面向客户的 web 控制台",
			Facts: domain.Facts{ObservedAt: time.Now(), DirSummary: []domain.DirEntry{
				{Name: "src", Summary: "前端代码"},
				{Name: "docs", Summary: "文档"},
				{Name: "scripts", Summary: "脚本"},
				{Name: "vendor", Summary: "依赖"},
			}},
		},
	}
}

// The digest is what lets the router know a workspace's recent life without a model
// call: it is derived from durable tickets on every request (design §4.6).
func TestDigestSummarisesRecentWork(t *testing.T) {
	now := time.Now()
	tickets := []domain.Ticket{
		{ID: "t1", Workspace: "ws_web", Title: "把登录超时改成可配置", CreatedAt: now.Add(-2 * time.Hour),
			Artifacts: []domain.Artifact{{ID: "a1", Title: "login.go"}}},
		{ID: "t2", Workspace: "ws_web", Title: "修一下账单页的空状态", CreatedAt: now.Add(-26 * time.Hour)},
		{ID: "t3", Workspace: "ws_web", Title: "补迁移脚本", CreatedAt: now.Add(-3 * 24 * time.Hour),
			Ended: &domain.Ended{At: now, Outcome: "done"}},
		{ID: "t4", Workspace: "ws_acme", Title: "别的区", CreatedAt: now},
		{ID: "t5", Workspace: "ws_web", Title: "很久以前", CreatedAt: now.Add(-30 * 24 * time.Hour)},
	}
	got := Digest(testWorkspace(), tickets, now, DigestMaxRunes)

	for _, want := range []string{"近两周 3 个工作项", "已完成 1", "登录超时", "账单页", "迁移脚本", "产出 1 个交付物", "目录：src"} {
		if !strings.Contains(got, want) {
			t.Fatalf("digest missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "别的区") || strings.Contains(got, "很久以前") {
		t.Fatalf("digest leaked other workspace or stale tickets:\n%s", got)
	}
	if n := len([]rune(got)); n > DigestMaxRunes {
		t.Fatalf("digest is %d runes, limit is %d", n, DigestMaxRunes)
	}
}

// A quiet workspace says so instead of inventing activity.
func TestDigestOfAQuietWorkspace(t *testing.T) {
	got := Digest(testWorkspace(), nil, time.Now(), DigestMaxRunes)
	if !strings.Contains(got, "没有工单") {
		t.Fatalf("quiet digest = %q", got)
	}
	if strings.Contains(got, "%!") {
		t.Fatalf("formatting bug: %q", got)
	}
}

// The digest is capped in runes, not bytes: a Chinese digest cut by bytes would
// produce invalid UTF-8 in the prompt.
func TestDigestClipsOnRuneBoundary(t *testing.T) {
	now := time.Now()
	var tickets []domain.Ticket
	for i := 0; i < 20; i++ {
		tickets = append(tickets, domain.Ticket{
			ID: domain.ID(string(rune('a' + i))), Workspace: "ws_web",
			Title:     strings.Repeat("很长的中文标题", 5),
			CreatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	got := Digest(testWorkspace(), tickets, now, 120)
	if len([]rune(got)) > 120 {
		t.Fatalf("digest is %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("clipped digest should say so: %q", got)
	}
	for _, r := range got {
		if r == '\uFFFD' {
			t.Fatalf("invalid UTF-8 after clipping: %q", got)
		}
	}
}

// The brief the router sees carries the digest.
func TestRenderIncludesTheDigest(t *testing.T) {
	out := RenderWith(testWorkspace(), time.Now(), "近两周 2 个工作项：登录、账单。")
	if !strings.Contains(out, "近况  近两周 2 个工作项") {
		t.Fatalf("digest missing from the brief:\n%s", out)
	}
	// Without a digest the line disappears rather than printing an empty label.
	if strings.Contains(Render(testWorkspace(), time.Now()), "近况") {
		t.Fatal("empty digest still rendered a label")
	}
}
