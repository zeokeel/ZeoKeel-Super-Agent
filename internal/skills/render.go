package skills

import (
	"fmt"
	"strings"
)

// minBlockBytes 是"截断后仍值得保留"的最小块大小。低于它就整块丢掉：
// 只剩标题和一句话的技能块对模型没有价值，还占预算。
const minBlockBytes = 200

// promptHeader 是注入块的固定开头。
//
// 它刻意写了两件事：这些技能是"候选"而不是"命令"，以及不要强行套用。
// 长任务里最贵的错误是模型为了"用上技能"而扭曲任务；显式的边界能明显降低它。
const promptHeader = `## 可用技能（Skills）

下面按相关度列出与本次任务相关的技能。只有当任务的实际情况确实落在「何时使用」范围内时才应用；不要因为技能存在就强行套用。需要完整内容时用 read 工具打开来源文件。

`

// RenderPrompt 是上层最常用的入口：给定任务文本，选出候选并渲染成可直接拼进
// 系统提示的一段文字。没有匹配时返回空字符串（调用方可以直接 if s != "" 再拼接）。
func (s *Set) RenderPrompt(task string, limit int) string {
	return s.Render(s.Match(task, limit))
}

// Render 把匹配结果渲染成提示词片段。
//
// 输出是确定性的：同样的 matches 一定得到同样的字节序列。这让"系统提示变了"
// 在 diff 里可见——排查模型行为变化时，这一条比什么都重要。
func (s *Set) Render(matches []Match) string {
	if len(matches) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(promptHeader)

	for i, m := range matches {
		if m.Skill == nil {
			continue
		}
		block := renderBlock(i+1, m)
		remaining := s.opts.MaxPromptBytes - b.Len()
		if remaining <= 0 {
			fmt.Fprintf(&b, "\n（还有 %d 个技能因提示预算不足未展开，可按名字用 skill 工具加载）\n", len(matches)-i)
			break
		}
		if len(block) <= remaining {
			b.WriteString(block)
			continue
		}
		// 预算不够整块时，尽量保留一个带正文的截断块——比只剩标题有用。
		if remaining >= minBlockBytes {
			b.WriteString(truncateBlock(block, remaining))
			fmt.Fprintf(&b, "\n（正文已截断；还有 %d 个技能因提示预算不足未展开）\n", len(matches)-i-1)
		} else {
			fmt.Fprintf(&b, "\n（还有 %d 个技能因提示预算不足未展开）\n", len(matches)-i)
		}
		break
	}
	return b.String()
}

// renderBlock 渲染单个技能。
func renderBlock(idx int, m Match) string {
	sk := m.Skill
	var b strings.Builder
	fmt.Fprintf(&b, "### %d. %s（相关度 %d）\n", idx, sk.Name, m.Score)
	fmt.Fprintf(&b, "- 描述：%s\n", sk.Description)
	fmt.Fprintf(&b, "- 何时使用：%s\n", sk.WhenToUse)
	if sk.WhenNotToUse != "" {
		fmt.Fprintf(&b, "- 何时不要使用：%s\n", sk.WhenNotToUse)
	} else {
		b.WriteString("- 何时不要使用：未显式声明；若任务与上面的范围只沾边，就不要套用\n")
	}
	fmt.Fprintf(&b, "- 来源：%s\n", sk.Source)
	if len(m.Reasons) > 0 {
		// 命中理由也写进去：模型看到"为什么选我"能更准确地判断是否该用。
		fmt.Fprintf(&b, "- 命中理由：%s\n", strings.Join(m.Reasons, "；"))
	}
	b.WriteString("\n")
	b.WriteString(sk.Body)
	b.WriteString("\n\n")
	return b.String()
}

// truncateBlock 在 UTF-8 字符边界上把块裁到 max 字节。strings.ToValidUTF8 会把
// 末尾被截断的半个字符去掉（std 库自 1.13 起提供），不再手写逐字节回退。
func truncateBlock(block string, max int) string {
	if len(block) <= max {
		return block
	}
	return strings.ToValidUTF8(block[:max], "") + "\n…（技能正文因提示预算已截断）\n"
}
