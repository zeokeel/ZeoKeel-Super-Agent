package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/zeokeel/super-agent/internal/sources"
)

// Members 实现可选的 sources.MemberLister：把仓库协作者拉成白名单候选。
//
// 为什么是协作者而不是"评论过的人"：能往仓库推代码的人才是能在仓库里发起
// 工作的人，这与 GitHub 的权限模型一致；机器人账号也一并在列表里，由 Policy
// 决定要不要放进 bot list。
func (s *Source) Members(ctx context.Context) ([]sources.Member, error) {
	// 协作者列表也可能超过一页：分页不看全，白名单会被悄悄裁掉。
	var users []apiUser
	for page := 1; ; page++ {
		if page > maxListPages {
			return nil, fmt.Errorf("github: collaborator list exceeded %d pages", maxListPages)
		}
		out, err := s.cmd.Run(ctx, "api",
			fmt.Sprintf("repos/%s/collaborators?per_page=%d&page=%d", s.cfg.Repository, listPageSize, page))
		if err != nil {
			return nil, fmt.Errorf("github: read collaborators: %w", err)
		}
		items, err := decodeRawList(out)
		if err != nil {
			return nil, fmt.Errorf("github: collaborators: %w", err)
		}
		pageUsers, unreadable, err := decodeItems[apiUser](items)
		if err != nil {
			return nil, fmt.Errorf("github: collaborators: %w", err)
		}
		if unreadable > 0 {
			// 白名单同步不能用一份缺人的名单覆盖 Policy：宁可整次失败让人重试。
			return nil, fmt.Errorf("github: %d collaborator records are unreadable", unreadable)
		}
		users = append(users, pageUsers...)
		if len(items) < listPageSize {
			break
		}
	}
	members := make([]sources.Member, 0, len(users))
	for _, user := range users {
		if strings.TrimSpace(user.Login) == "" {
			continue
		}
		members = append(members, sources.Member{ID: user.Login, Name: user.Login, Bot: IsBot(user.Login)})
	}
	return members, nil
}

// Health 实现可选的 sources.HealthReporter：问 gh 它是谁，再和配置的 agent
// 账号对一下。面板"来源"页需要一行能回答"这个 Source 现在能不能干活"。
//
// 只查账号不查仓库权限：账号不对是最常见的配置错误（gh 用了个人账号而不是
// agent 账号），仓库权限会在第一次轮询时以真实错误暴露出来。
func (s *Source) Health(ctx context.Context) sources.Health {
	at := s.now().UTC()
	out, err := s.cmd.Run(ctx, "api", "user", "--jq", ".login")
	if err != nil {
		return sources.Health{State: sources.HealthError, Detail: "gh CLI 调用失败：" + err.Error(), At: at}
	}
	login := strings.TrimSpace(string(out))
	if login == "" {
		return sources.Health{State: sources.HealthError, Detail: "gh 没有返回登录账号", At: at}
	}
	if !strings.EqualFold(login, s.cfg.Login) {
		// 这条错误必须说清两边是谁，否则用户只会看到"认证失败"。
		return sources.Health{State: sources.HealthError,
			Detail: fmt.Sprintf("gh 登录的是 @%s，配置的 agent 账号是 @%s", login, s.cfg.Login), At: at}
	}
	return sources.Health{State: sources.HealthReady,
		Detail: fmt.Sprintf("账号 @%s，仓库 %s", login, s.cfg.Repository), At: at}
}
