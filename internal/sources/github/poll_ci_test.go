package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/sources"
)

func ciCheck(name, status, conclusion string) map[string]any {
	run := map[string]any{"name": name, "status": status}
	if conclusion == "" {
		run["conclusion"] = nil
	} else {
		run["conclusion"] = conclusion
	}
	return run
}

// branchCIResponses 构造默认分支 CI 的假 gh，runs 由测试在轮询之间改。
func branchCIResponses(t *testing.T, runs *[]map[string]any) func([]string) ([]byte, error) {
	t.Helper()
	return func(args []string) ([]byte, error) {
		path := pathOf(args)
		switch {
		case strings.HasPrefix(path, "notifications"):
			return []byte("[]"), nil
		case path == "repos/acme/webapp":
			return []byte(`{"default_branch":"main"}`), nil
		case strings.HasPrefix(path, "commits/main"):
			return []byte(`{"sha":"abcdef1234567890"}`), nil
		case strings.Contains(path, "/check-runs"):
			body, err := json.Marshal(map[string]any{"total_count": len(*runs), "check_runs": *runs})
			if err != nil {
				t.Fatalf("marshal checks: %v", err)
			}
			return body, nil
		default:
			t.Errorf("unexpected gh call: %v", args)
			return nil, fmt.Errorf("unexpected gh call: %v", args)
		}
	}
}

// 默认分支 CI：第一次只建基线，失败集合变化才发一条，恢复也只报一次；
// 检查还在跑时不把"暂时没失败"当成恢复（原型 pollBranchCi）。
func TestPollBranchCITracksFailureSetChanges(t *testing.T) {
	runs := []map[string]any{
		ciCheck("unit", "completed", "success"),
		ciCheck("lint", "completed", "failure"),
	}
	cmd := &fakeCommand{respond: branchCIResponses(t, &runs)}
	src := testSource(t, cmd, func(cfg *Config) { cfg.ObserveCI = true })
	var collector receiveCollector

	// 1. 第一次观察：只建基线，不发 Trigger。
	res, err := src.Poll(context.Background(), sources.PollRequest{}, collector.receive)
	if err != nil {
		t.Fatalf("baseline poll: %v", err)
	}
	if res.Received != 0 {
		t.Fatalf("baseline received %d triggers, want 0", res.Received)
	}
	state, ok := decodeCursor(res.Next)
	if !ok || state.CIBranch != "main" || !equalStrings(state.CIFailures, []string{"lint"}) {
		t.Fatalf("baseline cursor = %+v (ok=%v)", state, ok)
	}

	// 2. 同一批检查：不再发。
	res, err = src.Poll(context.Background(), sources.PollRequest{Cursor: res.Next}, collector.receive)
	if err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if res.Received != 0 {
		t.Fatalf("unchanged CI received %d triggers, want 0", res.Received)
	}

	// 3. 出现新的失败：发一条，revision 递增，Thread 是分支级 CI Thread。
	runs = append(runs, ciCheck("build", "completed", "failure"))
	baseline := res.Next
	res, err = src.Poll(context.Background(), sources.PollRequest{Cursor: baseline}, collector.receive)
	if err != nil {
		t.Fatalf("changed poll: %v", err)
	}
	if res.Received != 1 || len(collector.triggers) != 1 {
		t.Fatalf("CI change received %d triggers, want 1", res.Received)
	}
	tr := collector.triggers[0]
	if tr.Channel.Address["kind"] != RawCI || tr.Channel.Address["revision"] != "1" {
		t.Fatalf("trigger address = %v", tr.Channel.Address)
	}
	if tr.Threads[0].ID != "acme/webapp#ci:main" {
		t.Fatalf("CI thread = %q", tr.Threads[0].ID)
	}
	if tr.Human || tr.Addressed.AddressesAgent() {
		t.Fatal("CI is not a human request and must not be addressed")
	}
	if !strings.Contains(tr.Text, "CI 失败 2/3") {
		t.Fatalf("CI text = %q", tr.Text)
	}
	if scope := src.Scope(tr); !scope.Listened || !scope.Observed || scope.Row != "ci" {
		t.Fatalf("CI scope = %+v", scope)
	}

	// 4. 检查还在跑：保留旧失败集合，不报"恢复"。
	runs = []map[string]any{
		ciCheck("unit", "completed", "success"),
		ciCheck("lint", "completed", "success"),
		ciCheck("build", "in_progress", ""),
	}
	res, err = src.Poll(context.Background(), sources.PollRequest{Cursor: res.Next}, collector.receive)
	if err != nil {
		t.Fatalf("running poll: %v", err)
	}
	if res.Received != 0 {
		t.Fatalf("running CI received %d triggers, want 0 (must not report recovery yet)", res.Received)
	}

	// 5. 全部通过：报一次恢复，revision 继续递增。
	runs = []map[string]any{
		ciCheck("unit", "completed", "success"),
		ciCheck("lint", "completed", "success"),
		ciCheck("build", "completed", "success"),
	}
	res, err = src.Poll(context.Background(), sources.PollRequest{Cursor: res.Next}, collector.receive)
	if err != nil {
		t.Fatalf("recovery poll: %v", err)
	}
	if res.Received != 1 || len(collector.triggers) != 2 {
		t.Fatalf("CI recovery received %d triggers, want 1", res.Received)
	}
	recovery := collector.triggers[1]
	if recovery.Channel.Address["revision"] != "2" || !strings.Contains(recovery.Text, "CI 通过") {
		t.Fatalf("recovery trigger = %v / %q", recovery.Channel.Address, recovery.Text)
	}
}

// CI 读取失败必须让整轮失败并保持游标：下一轮重试，不能跳过一段 CI 历史。
func TestPollBranchCIFailureKeepsCursor(t *testing.T) {
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		path := pathOf(args)
		switch {
		case strings.HasPrefix(path, "notifications"):
			return []byte("[]"), nil
		case path == "repos/acme/webapp":
			return []byte(`{"default_branch":"main"}`), nil
		case strings.HasPrefix(path, "commits/main"):
			return []byte(`{"sha":"abcdef1234567890"}`), nil
		default:
			return nil, fmt.Errorf("boom")
		}
	}}
	src := testSource(t, cmd, func(cfg *Config) { cfg.ObserveCI = true })
	var collector receiveCollector
	cursor := encodeCursor("acme/webapp", testNow.Add(-time.Hour))
	res, err := src.Poll(context.Background(), sources.PollRequest{Cursor: cursor}, collector.receive)
	if err == nil {
		t.Fatal("expected the CI read failure to fail the round")
	}
	if res.Next != cursor {
		t.Fatalf("cursor advanced on failure: %q -> %q", cursor, res.Next)
	}
}
