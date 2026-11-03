package goal

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/store"
)

func openService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "goal.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	s, err := New(st, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return s, st
}

func mustCreate(t *testing.T, s *Service, objective string, maxRounds int) Goal {
	t.Helper()
	g, err := s.Create("ws_a", objective, maxRounds)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return g
}

func TestCreateValidationAndIdempotency(t *testing.T) {
	s, _ := openService(t)
	g, err := s.Create("ws_a", "把 acme 的月报做成周报", 5)
	if err != nil {
		t.Fatal(err)
	}
	if g.State != Active || g.Revision != 1 || g.BlockThreshold != DefaultBlockThreshold ||
		g.MaxRounds != 5 || len(g.Rounds) != 0 || g.Objective == "" {
		t.Fatalf("new goal = %+v", g)
	}
	again, err := s.Create("ws_a", "把 acme 的月报做成周报", 9)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != g.ID || again.Revision != 1 || again.MaxRounds != 5 {
		t.Fatalf("create is not idempotent: %+v vs %+v", again, g)
	}
	if _, err := s.Create("ws_a", "  ", 3); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty objective err = %v", err)
	}
	if _, err := s.Create("ws_a", "另一个目标", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero max rounds err = %v", err)
	}
	// Same objective in another workspace is a different goal (scope is in the ID).
	other, err := s.Create("ws_b", "把 acme 的月报做成周报", 5)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == g.ID {
		t.Fatal("workspace scope missing from goal id")
	}
}

func TestPauseResumeCompleteTransitions(t *testing.T) {
	s, _ := openService(t)
	g := mustCreate(t, s, "整理登录超时的修复方案", 5)

	paused, err := s.Pause(g.ID, g.Revision)
	if err != nil || paused.State != Paused || paused.Revision != 2 {
		t.Fatalf("pause = %+v err=%v", paused, err)
	}
	if _, err := s.Advance(g.ID, paused.Revision, RoundInput{Summary: "x"}); !errors.Is(err, ErrNotActive) {
		t.Fatalf("advance while paused err = %v", err)
	}
	active, err := s.Resume(g.ID, paused.Revision)
	if err != nil || active.State != Active || active.Revision != 3 {
		t.Fatalf("resume = %+v err=%v", active, err)
	}
	paused2, err := s.Pause(g.ID, active.Revision)
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.Complete(g.ID, paused2.Revision, "operator 决定收尾")
	if err != nil || done.State != Completed || done.EndReason != "operator 决定收尾" {
		t.Fatalf("complete = %+v err=%v", done, err)
	}
	if _, err := s.Pause(g.ID, done.Revision); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("pause completed err = %v", err)
	}
	if _, err := s.Resume(g.ID, done.Revision); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("resume completed err = %v", err)
	}
}

func TestAdvanceAppendsRoundsWithEvidence(t *testing.T) {
	s, _ := openService(t)
	g := mustCreate(t, s, "升级依赖并跑通测试", 5)
	g1, err := s.Advance(g.ID, g.Revision, RoundInput{
		Summary: "读完 go.mod", Evidence: []string{"go.mod", "go.sum"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if g1.Revision != 2 || len(g1.Rounds) != 1 || g1.Rounds[0].Index != 1 ||
		g1.Rounds[0].Summary != "读完 go.mod" || len(g1.Rounds[0].Evidence) != 2 {
		t.Fatalf("round 1 = %+v", g1)
	}
	g2, err := s.Advance(g.ID, g1.Revision, RoundInput{Summary: "升级完成", Evidence: []string{"go test ./... · PASS"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(g2.Rounds) != 2 || g2.Rounds[1].Index != 2 || g2.State != Active {
		t.Fatalf("round 2 = %+v", g2)
	}
	if _, err := s.Advance(g.ID, g2.Revision-1, RoundInput{Summary: "stale"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale advance err = %v", err)
	}
}

func TestRoundBudgetCompletesTheGoal(t *testing.T) {
	s, _ := openService(t)
	g := mustCreate(t, s, "两轮内解决 CI 失败", 2)
	g, err := s.Advance(g.ID, g.Revision, RoundInput{Summary: "定位失败"})
	if err != nil {
		t.Fatal(err)
	}
	if g.State != Active {
		t.Fatalf("round 1 state = %s", g.State)
	}
	g, err = s.Advance(g.ID, g.Revision, RoundInput{Summary: "修复并验证"})
	if err != nil {
		t.Fatal(err)
	}
	if g.State != Completed || !strings.Contains(g.EndReason, "轮次上限") {
		t.Fatalf("after budget = %+v", g)
	}
	if _, err := s.Advance(g.ID, g.Revision, RoundInput{Summary: "over"}); !errors.Is(err, ErrNotActive) {
		t.Fatalf("advance after complete err = %v", err)
	}
}

func TestSameBlockConditionBlocksAfterThreshold(t *testing.T) {
	s, _ := openService(t)
	g := mustCreate(t, s, "让 CI 通过", 10)
	for i := 1; i <= 2; i++ {
		var err error
		g, err = s.Advance(g.ID, g.Revision, RoundInput{Summary: "重试", Blocked: "测试环境不可达"})
		if err != nil {
			t.Fatal(err)
		}
		if g.State != Active {
			t.Fatalf("round %d blocked too early: %+v", i, g)
		}
		if g.ConsecutiveBlocked != i {
			t.Fatalf("round %d consecutive = %d", i, g.ConsecutiveBlocked)
		}
	}
	g, err := s.Advance(g.ID, g.Revision, RoundInput{Summary: "第三次重试", Blocked: "测试环境不可达"})
	if err != nil {
		t.Fatal(err)
	}
	if g.State != Blocked || !strings.Contains(g.BlockedReason, "测试环境不可达") ||
		!strings.Contains(g.BlockedReason, "3") {
		t.Fatalf("third block = %+v", g)
	}
}

func TestDifferentBlockConditionResetsCount(t *testing.T) {
	s, _ := openService(t)
	g := mustCreate(t, s, "多步修复", 10)
	steps := []string{"A", "A", "B", "B", "", "B"}
	for i, cond := range steps {
		var err error
		g, err = s.Advance(g.ID, g.Revision, RoundInput{Summary: "round", Blocked: cond})
		if err != nil {
			t.Fatal(err)
		}
		if g.State == Blocked {
			t.Fatalf("step %d (%q) blocked too early: %+v", i, cond, g)
		}
	}
	if g.ConsecutiveBlocked != 1 || g.BlockedCondition != "B" {
		t.Fatalf("after reset then B: consecutive=%d condition=%q", g.ConsecutiveBlocked, g.BlockedCondition)
	}
}

func TestProgressRoundResetsBlockAccounting(t *testing.T) {
	s, _ := openService(t)
	g := mustCreate(t, s, "先卡后通", 10)
	for _, cond := range []string{"网络", "网络"} {
		var err error
		g, err = s.Advance(g.ID, g.Revision, RoundInput{Summary: "r", Blocked: cond})
		if err != nil {
			t.Fatal(err)
		}
	}
	g, err := s.Advance(g.ID, g.Revision, RoundInput{Summary: "有进展了"})
	if err != nil {
		t.Fatal(err)
	}
	if g.ConsecutiveBlocked != 0 || g.BlockedCondition != "" {
		t.Fatalf("productive round did not reset: %+v", g)
	}
	g, err = s.Advance(g.ID, g.Revision, RoundInput{Summary: "又卡", Blocked: "网络"})
	if err != nil {
		t.Fatal(err)
	}
	if g.State == Blocked || g.ConsecutiveBlocked != 1 {
		t.Fatalf("count after reset = %+v", g)
	}
}

func TestEditLimitsAndObjectiveImmutable(t *testing.T) {
	s, _ := openService(t)
	g := mustCreate(t, s, "保持目标不变的编辑", 3)
	edited, err := s.Edit(g.ID, g.Revision, func(e *Editable) error {
		e.MaxRounds = 5
		e.BlockThreshold = 2
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited.MaxRounds != 5 || edited.BlockThreshold != 2 || edited.Revision != 2 ||
		edited.Objective != g.Objective {
		t.Fatalf("edited = %+v", edited)
	}
	if _, err := s.Edit(g.ID, g.Revision, func(e *Editable) error { e.MaxRounds = 9; return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale edit err = %v", err)
	}
	// The edited threshold takes effect immediately: two same blocks now suffice.
	edited, err = s.Advance(g.ID, edited.Revision, RoundInput{Summary: "1", Blocked: "磁盘满"})
	if err != nil {
		t.Fatal(err)
	}
	edited, err = s.Advance(g.ID, edited.Revision, RoundInput{Summary: "2", Blocked: "磁盘满"})
	if err != nil {
		t.Fatal(err)
	}
	if edited.State != Blocked {
		t.Fatalf("threshold edit not applied: %+v", edited)
	}
	if _, err := s.Edit(g.ID, edited.Revision, func(e *Editable) error { e.MaxRounds = 1; return nil }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("max below rounds err = %v", err)
	}
}

func TestResumeBlockedResetsAccounting(t *testing.T) {
	s, _ := openService(t)
	g := mustCreate(t, s, "解阻后继续", 10)
	g, err := s.Edit(g.ID, g.Revision, func(e *Editable) error { e.BlockThreshold = 2; return nil })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		g, err = s.Advance(g.ID, g.Revision, RoundInput{Summary: "blocked", Blocked: "权限不足"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if g.State != Blocked {
		t.Fatalf("not blocked: %+v", g)
	}
	g, err = s.Resume(g.ID, g.Revision)
	if err != nil || g.State != Active || g.ConsecutiveBlocked != 0 || g.BlockedReason != "" {
		t.Fatalf("resume blocked = %+v err=%v", g, err)
	}
	g, err = s.Advance(g.ID, g.Revision, RoundInput{Summary: "again", Blocked: "权限不足"})
	if err != nil || g.State == Blocked || g.ConsecutiveBlocked != 1 {
		t.Fatalf("count after resume = %+v err=%v", g, err)
	}
}

func TestRestartRestoresGoalFromStore(t *testing.T) {
	s, st := openService(t)
	g := mustCreate(t, s, "重启后接着跑", 4)
	paused, err := s.Pause(g.ID, g.Revision)
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := s.Advance(g.ID, paused.Revision, RoundInput{Summary: "half"})
	if !errors.Is(err, ErrNotActive) {
		t.Fatalf("paused advance err = %v", err)
	}
	_ = advanced
	// New service on the same file is the restarted process.
	s2, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := s2.Get(g.ID)
	if err != nil || !ok {
		t.Fatalf("get after restart: ok=%v err=%v", ok, err)
	}
	if got.State != Paused || got.Revision != paused.Revision || got.Objective != g.Objective ||
		len(got.Rounds) != 0 || !got.CreatedAt.Equal(g.CreatedAt) {
		t.Fatalf("restored = %+v", got)
	}
	resumed, err := s2.Resume(got.ID, got.Revision)
	if err != nil || resumed.State != Active {
		t.Fatalf("resume after restart = %+v err=%v", resumed, err)
	}
}

func TestListByWorkspaceNewestFirst(t *testing.T) {
	s, _ := openService(t)
	a1 := mustCreate(t, s, "目标一", 3)
	_, _ = s.Create("ws_a", "目标二", 3)
	if _, err := s.Create("ws_b", "目标三", 3); err != nil {
		t.Fatal(err)
	}
	// Advance 目标一 so it becomes the most recently updated goal in ws_a.
	g, err := s.Advance(a1.ID, a1.Revision, RoundInput{Summary: "第一步"})
	if err != nil {
		t.Fatal(err)
	}
	if g.UpdatedAt.Before(a1.UpdatedAt) {
		t.Fatalf("update time did not move: %v -> %v", a1.UpdatedAt, g.UpdatedAt)
	}
	wsA, err := s.List("ws_a")
	if err != nil || len(wsA) != 2 {
		t.Fatalf("ws_a list = %d err=%v", len(wsA), err)
	}
	if wsA[0].ID != a1.ID {
		t.Fatalf("order = %+v, want advanced goal first", wsA)
	}
	all, err := s.List("")
	if err != nil || len(all) != 3 {
		t.Fatalf("all list = %d err=%v", len(all), err)
	}
}
