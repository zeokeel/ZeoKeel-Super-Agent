package github

import (
	"testing"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/sources"
)

// translated 翻译一条评论，供 Scope 的测试复用。
func translated(t *testing.T, src *Source, id int64, login, body string, number int) domain.Trigger {
	t.Helper()
	tr, err := src.Translate(sources.Raw{
		Kind: RawComment,
		Data: commentElement(t, id, login, body, number),
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	return tr
}

func TestScopeListensToAddressedComment(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	tr := translated(t, src, 1, "alice", "@zeokeel-agent 请修", 12)

	scope := src.Scope(tr)
	if !scope.Listened {
		t.Fatal("an addressed comment was not listened to")
	}
	if scope.Observed {
		t.Fatal("an addressed comment was marked Observed; only unaddressed receive-all is observed")
	}
	if scope.Row != RowComment {
		t.Fatalf("Row = %q, want comment", scope.Row)
	}
}

func TestScopeFiltersUnaddressedCommentByDefault(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	tr := translated(t, src, 2, "alice", "这个接口我明天改", 12)

	if scope := src.Scope(tr); scope.Listened {
		t.Fatalf("an unaddressed comment was received with ObserveComments off: %+v", scope)
	}
}

func TestScopeObservesUnaddressedWhenConfigured(t *testing.T) {
	src := testSource(t, &fakeCommand{}, func(cfg *Config) { cfg.ObserveComments = true })
	tr := translated(t, src, 3, "alice", "这个接口我明天改", 12)

	scope := src.Scope(tr)
	if !scope.Listened {
		t.Fatal("ObserveComments was on but the comment was not listened to")
	}
	if !scope.Observed {
		t.Fatal("receive-all mode must mark the row Observed so the admission table can decide")
	}
	if scope.Row != RowComment {
		t.Fatalf("Row = %q", scope.Row)
	}
}

func TestScopeRejectsOtherRepository(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	tr := translated(t, src, 4, "alice", "@zeokeel-agent 请修", 12)

	// 把回信地址与 Thread 都改成另一个仓库：哪怕正文 @ 了 agent，也不该被
	// 本 Source 接收。
	tr.Channel.Address["repository"] = "other/repo"
	tr.Threads[0].ID = "other/repo#12"
	tr.Channel.Thread = "other/repo#12"

	if scope := src.Scope(tr); scope.Listened {
		t.Fatalf("a comment from another repository was listened to: %+v", scope)
	}
}

func TestScopeRejectsForeignSource(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	tr := translated(t, src, 5, "alice", "@zeokeel-agent 请修", 12)
	tr.Source = "feishu"

	if scope := src.Scope(tr); scope.Listened {
		t.Fatalf("Scope accepted a Trigger from another Source: %+v", scope)
	}
}

func TestScopeFallsBackToThreadIDWhenAddressIsEmpty(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	tr := translated(t, src, 6, "alice", "@zeokeel-agent 请修", 12)
	tr.Channel = domain.ChannelRef{Source: "github", Thread: "acme/webapp#12"}

	if scope := src.Scope(tr); !scope.Listened {
		t.Fatalf("Scope dropped a Trigger whose repository is only in the Thread id: %+v", scope)
	}
}

func TestScopeIgnoresMalformedThread(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	tr := domain.Trigger{Source: "github", Threads: []domain.ThreadRef{{Source: "github", ID: "not-a-thread"}}}
	if scope := src.Scope(tr); scope.Listened {
		t.Fatalf("Scope accepted a Trigger with a malformed Thread id: %+v", scope)
	}
}
