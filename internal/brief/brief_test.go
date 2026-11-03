package brief

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

func ws(id, name string, keywords ...string) domain.Workspace {
	return domain.Workspace{
		ID:   domain.WorkspaceID(id),
		Name: name,
		Kind: domain.KindData,
		Brief: domain.Brief{
			Purpose:  name + " 的客户数据",
			Keywords: keywords,
		},
	}
}

// A customer name buried in a Chinese sentence must find its workspace.
func TestRecallFindsWorkspaceInsideSentence(t *testing.T) {
	ix := NewIndex([]domain.Workspace{
		ws("ws_acme", "acme-data", "acme", "用量", "计费"),
		ws("ws_beta", "beta-data", "beta", "报表"),
	})
	got := ix.Recall("帮我把 acme 这个客户上个月的用量数据发我一下")
	if len(got) == 0 || got[0].ID != "ws_acme" {
		t.Fatalf("expected ws_acme first, got %+v", got)
	}
}

// A miss must return nothing rather than the closest guess; the Router asks a
// person instead of picking at random.
func TestRecallReturnsNothingOnMiss(t *testing.T) {
	ix := NewIndex([]domain.Workspace{ws("ws_acme", "acme-data", "acme")})
	if got := ix.Recall("今天中午吃什么"); len(got) != 0 {
		t.Fatalf("expected no candidates, got %+v", got)
	}
}

func TestRecallRanksByMatchedTerms(t *testing.T) {
	ix := NewIndex([]domain.Workspace{
		ws("ws_a", "a", "acme"),
		ws("ws_b", "b", "acme", "用量"),
	})
	got := ix.Recall("acme 的 用量")
	if len(got) < 2 || got[0].ID != "ws_b" {
		t.Fatalf("expected ws_b first (two terms), got %+v", got)
	}
}

// The rendered brief carries the fields the Router needs and flags staleness.
func TestRenderIncludesStaleness(t *testing.T) {
	w := ws("ws_acme", "acme-data", "acme")
	w.Brief.Constraints = []string{"只发汇总，不发明细"}
	w.Brief.Facts.ObservedAt = time.Now().Add(-30 * 24 * time.Hour)
	out := Render(w, time.Now())
	for _, want := range []string{"ws_acme", "目的", "约束", "只发汇总", "过期"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered brief missing %q:\n%s", want, out)
		}
	}
}
