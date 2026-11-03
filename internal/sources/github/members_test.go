package github

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zeokeel/super-agent/internal/sources"
)

func TestMembersListsCollaboratorsAndMarksBots(t *testing.T) {
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		if path := pathOf(args); strings.HasPrefix(path, "repos/acme/webapp/collaborators") {
			return []byte(`[
				{"login":"alice","type":"User"},
				{"login":"dependabot[bot]","type":"Bot"}
			]`), nil
		}
		return nil, errors.New("unexpected gh call")
	}}
	src := testSource(t, cmd, nil)

	members, err := src.Members(context.Background())
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("Members returned %d entries, want 2", len(members))
	}
	if members[0].ID != "alice" || members[0].Bot {
		t.Fatalf("members[0] = %+v", members[0])
	}
	if members[1].ID != "dependabot[bot]" || !members[1].Bot {
		t.Fatalf("members[1] = %+v", members[1])
	}
}

func TestMembersRefusesAPartiallyUnreadableRoster(t *testing.T) {
	// 白名单同步不能用缺人的名单覆盖 Policy：一次坏数据就整次失败。
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		return []byte(`[{"login":123}]`), nil
	}}
	src := testSource(t, cmd, nil)

	if _, err := src.Members(context.Background()); err == nil {
		t.Fatal("Members accepted a roster with an unreadable entry")
	}
}

func TestHealthReadyWhenGHAgreesWithConfig(t *testing.T) {
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		return []byte("zeokeel-agent\n"), nil
	}}
	src := testSource(t, cmd, nil)

	health := src.Health(context.Background())
	if health.State != sources.HealthReady {
		t.Fatalf("Health = %+v, want ready", health)
	}
	if !strings.Contains(health.Detail, "zeokeel-agent") || !strings.Contains(health.Detail, "acme/webapp") {
		t.Fatalf("Health detail = %q, want account and repository", health.Detail)
	}
	if !health.At.Equal(testNow) {
		t.Fatalf("Health.At = %v, want the pinned clock", health.At)
	}
}

func TestHealthReportsWrongAccount(t *testing.T) {
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		return []byte("someone-else\n"), nil
	}}
	src := testSource(t, cmd, nil)

	health := src.Health(context.Background())
	if health.State != sources.HealthError {
		t.Fatalf("Health = %+v, want error", health)
	}
	// 错误必须同时说出 gh 实际登录谁、配置期望谁，否则用户没法修。
	if !strings.Contains(health.Detail, "someone-else") || !strings.Contains(health.Detail, "zeokeel-agent") {
		t.Fatalf("Health detail = %q, want both logins", health.Detail)
	}
}

func TestHealthReportsCLIAndEmptyOutput(t *testing.T) {
	failing := testSource(t, &fakeCommand{respond: func(args []string) ([]byte, error) {
		return nil, errors.New("gh not found")
	}}, nil)
	if health := failing.Health(context.Background()); health.State != sources.HealthError {
		t.Fatalf("Health = %+v, want error when gh fails", health)
	}

	empty := testSource(t, &fakeCommand{respond: func(args []string) ([]byte, error) {
		return []byte("  \n"), nil
	}}, nil)
	if health := empty.Health(context.Background()); health.State != sources.HealthError {
		t.Fatalf("Health = %+v, want error on empty login", health)
	}
}

// 协作者列表可能超过一页：分页不看全，白名单会被悄悄裁掉。
func TestMembersPaginatesCollaborators(t *testing.T) {
	page := func(start, n int) string {
		items := make([]string, 0, n)
		for i := start; i < start+n; i++ {
			items = append(items, fmt.Sprintf(`{"login":"user%d","type":"User"}`, i))
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		path := pathOf(args)
		if !strings.HasPrefix(path, "repos/acme/webapp/collaborators") {
			return nil, fmt.Errorf("unexpected gh call: %v", args)
		}
		if strings.Contains(path, "&page=2") {
			return []byte(page(100, 2)), nil
		}
		return []byte(page(0, listPageSize)), nil
	}}
	src := testSource(t, cmd, nil)
	members, err := src.Members(context.Background())
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != listPageSize+2 {
		t.Fatalf("Members = %d, want %d", len(members), listPageSize+2)
	}
	if members[len(members)-1].ID != "user101" {
		t.Fatalf("last member = %+v, want user101 from page 2", members[len(members)-1])
	}
}

// 第二页出现坏数据同样要整次失败：不能拿缺人的名单覆盖 Policy。
func TestMembersRefusesUnreadableLaterPage(t *testing.T) {
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		path := pathOf(args)
		if strings.Contains(path, "&page=2") {
			return []byte(`[{"login":123}]`), nil
		}
		items := make([]string, 0, listPageSize)
		for i := 0; i < listPageSize; i++ {
			items = append(items, fmt.Sprintf(`{"login":"user%d","type":"User"}`, i))
		}
		return []byte("[" + strings.Join(items, ",") + "]"), nil
	}}
	src := testSource(t, cmd, nil)
	if _, err := src.Members(context.Background()); err == nil {
		t.Fatal("Members accepted a roster with an unreadable second page")
	}
}
