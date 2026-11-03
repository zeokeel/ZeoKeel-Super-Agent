package github

import (
	"strconv"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/sources"
)

// Scope 判定一条 Trigger 是否落在本 Source 的接收范围内。
//
// 两道判定，都是确定性的、配置驱动的（§4.3 的 Gate 是事实层，不调用模型）：
//
//  1. 仓库对不对。Thread id 里带着仓库，所以别人的仓库、fork 都不会混进来。
//  2. 有没有明确对 agent 说话。没 @ agent 的评论默认只算"仓库里的讨论"，
//     不在接收范围——Source 侧就丢掉，连 Trigger 都不产生。打开
//     ObserveComments 后才收下并标记 Observed，由 Policy 的 admission table
//     决定是否成为候选（§4.9：宁可漏接，也不误接）。
func (s *Source) Scope(t domain.Trigger) sources.ScopeResult {
	if t.Source != string(Name) {
		return sources.ScopeResult{}
	}
	repo, _, ok := triggerRepository(&t)
	if !ok || !sameRepository(repo, s.cfg.Repository) {
		return sources.ScopeResult{}
	}

	row := admissionRow(t.Channel.Address["kind"])
	observed := s.observes(row)
	listened := t.Addressed.AddressesAgent() || observed
	if !listened {
		return sources.ScopeResult{}
	}
	return sources.ScopeResult{
		Listened: true,
		// observed 是"这一行配置成接收全部"的事实，而不是这条消息刚好没 @。
		// Gate 拿它判"名单外未 @"那一格是否需要 observed=true（§4.3）。
		Observed: observed,
		Row:      row,
	}
}

// observes 报告某一行（comments/issues/pulls）是否配置为"接收全部"。
// ObserveComments 是旧配置，保留兼容；ReceiveAll 隐含所有行。
func (s *Source) observes(row string) bool {
	switch row {
	case RowComment:
		return s.cfg.ObserveComments || s.cfg.ReceiveAll
	case RowIssue, RowPull:
		return s.cfg.ReceiveAll
	case RowCI:
		return s.cfg.ObserveCI
	default:
		return false
	}
}

// triggerRepository 从 Trigger 里找回仓库名：优先用回信地址（Channel 里
// 原样带着），退回解析 Thread id。两者都没有时说明这条 Trigger 不是本
// Source 能认的。
func triggerRepository(t *domain.Trigger) (string, int, bool) {
	if repo := t.Channel.Address["repository"]; repo != "" {
		if _, number, ok := SplitThreadID(t.Channel.Thread); ok {
			return repo, number, true
		}
		if number, err := strconv.Atoi(t.Channel.Address["number"]); err == nil && number > 0 {
			return repo, number, true
		}
		// CI Thread（repo#ci:branch）没有 Issue 号：仓库是唯一必要的事实，
		// 编号返回 0 表示"不是 Issue/PR 粒度"。
		return repo, 0, true
	}
	return firstThread(t)
}

// firstThread 解析 Trigger 的第一个 Thread。设计里 threads[0] 是主线程。
func firstThread(t *domain.Trigger) (string, int, bool) {
	for _, ref := range t.Threads {
		if ref.Source != string(Name) {
			continue
		}
		return SplitThreadID(ref.ID)
	}
	return "", 0, false
}

// admissionRow 把事件形状映射成 admission table 的行名。新事件形状（Issue
// 正文、CI）以后在这里加映射，Gate 不需要改。
func admissionRow(kind string) string {
	switch kind {
	case RawComment, RawReviewComment:
		return RowComment
	case RawIssue:
		return RowIssue
	case RawPull:
		return RowPull
	case RawCI:
		return RowCI
	default:
		return ""
	}
}
