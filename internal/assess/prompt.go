package assess

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zeokeel/super-agent/internal/prompts"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
)

// systemPrompt 是研判者的定位。它只回答"接不接、派给谁"，不执行、不说话——
// 与 Worker 的提示词完全分开（design §5.3：能力差异用工具白名单和提示词表达）。

// buildPrompt 把一条 Trigger、候选工作区简报与规则拼成研判输入。
//
// 简报是"缓存不是真相"，所以提示词里明确让模型以简报为界、拿不准就问——不展开
// 仓库内容（design §4.6：浪费上下文窗口就是浪费模型的智能）。
func buildPrompt(cfg Config, in engine.AssessInput) string {
	var b strings.Builder

	b.WriteString("## 收到的消息\n")
	fmt.Fprintf(&b, "来源：%s\n", in.Trigger.Source)
	if in.Trigger.Self {
		b.WriteString("发送者：operator 本人\n")
	} else {
		fmt.Fprintf(&b, "发送者：%s\n", in.Trigger.Actor.ID)
	}
	if in.Trigger.Addressed.Mention {
		b.WriteString("是否明确对 agent 说话：是（@）\n")
	} else if in.Trigger.Addressed.Direct {
		b.WriteString("是否明确对 agent 说话：是（单聊）\n")
	} else {
		b.WriteString("是否明确对 agent 说话：否\n")
	}
	if in.Trigger.Conversation.Name != "" {
		fmt.Fprintf(&b, "会话：%s（%s）\n", in.Trigger.Conversation.Name, in.Trigger.Conversation.Kind)
	}
	fmt.Fprintf(&b, "正文：%s\n", in.Trigger.Text)

	if len(in.Tickets) > 0 {
		b.WriteString("\n## 这条 Thread 上已有的工单\n")
		for _, t := range in.Tickets {
			state := "进行中"
			if t.Ended != nil {
				state = "已结束（" + t.Ended.Outcome + "）"
			}
			fmt.Fprintf(&b, "- #%d %s（%s）\n", t.Number, t.Title, state)
		}
		b.WriteString("已有工单覆盖的请求不要重复开单；确属新工作才 accept。\n")
	}

	b.WriteString("\n## 候选工作区\n")
	ids := make([]string, 0, len(in.Candidates))
	for _, c := range in.Candidates {
		ids = append(ids, string(c.ID))
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		b.WriteString("（没有任何候选。只能 kind=ask 或 kind=ignore）\n")
	}
	for _, id := range ids {
		b.WriteString("\n")
		b.WriteString(in.Briefs[domain.WorkspaceID(id)])
		b.WriteString("\n")
	}

	b.WriteString("\n## 启用的规则\n")
	enabled := 0
	for _, r := range ruleSet(cfg, in) {
		if !r.Enabled {
			continue
		}
		enabled++
		fmt.Fprintf(&b, "- id=%s：%s\n", r.ID, r.Instruction)
	}
	if enabled == 0 {
		b.WriteString("（没有启用规则。因此不能 accept，只能 ignore 或 ask）\n")
	}
	b.WriteString("\n现在给出 JSON。")
	return b.String()
}

// SystemPrompt is the built-in Router prompt, exported so the panel can show it as
// the default an edit is compared against (design §5.3). The text lives in
// internal/prompts so every prompt in the product is composed the same way.
func SystemPrompt() string { return prompts.RouterPrompt() }
