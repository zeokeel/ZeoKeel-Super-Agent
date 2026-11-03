package agent

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// compactInstruction 是压缩时给摘要模型的指令。
//
// 它决定了"模型丢了原文之后还记得什么"：无人值守长任务里，文件路径、已经做出的
// 决定、未完成事项是继续工作的最小必要信息；寒暄和工具原始输出必须丢，否则压缩
// 没有收益（design §5.4）。
const compactInstruction = `把上面的对话压缩成一份可独立阅读的工作摘要，供后续继续执行任务使用。
必须保留：用户的目标与约束、已经做出的决定及理由、涉及的文件路径与命令、已经验证过的事实、未完成事项与下一步。
不要保留：寒暄、重复解释、工具原始输出（只保留结论）。
不要新增事实。用中文输出，控制在 400 字以内。`

// newSummarizer 用官方 summarization 中间件做上下文压缩：超预算时把早期消息
// 摘要成一条消息，保留最近的消息原文。
//
// 选官方实现而不是自己写 MessageRewriter：压缩要考虑 token 估算、触发时机、
// system 消息保留、摘要后与消息链的拼接，这些官方实现已经处理；我们只挂一个
// Callback 把"压缩发生了"上报面板（面板需要这条事件，它不读中间件内部状态）。
func (r *Runner) newSummarizer(ctx context.Context,
	mb model.BaseModel[*schema.Message]) (adk.ChatModelAgentMiddleware, error) {
	return summarization.New(ctx, &summarization.Config{
		Model: mb,
		Trigger: &summarization.TriggerCondition{
			ContextTokens:   r.cfg.ContextBudget,
			ContextMessages: r.cfg.CompactAfterMessages,
		},
		UserInstruction: compactInstruction,
		Callback: func(cbCtx context.Context, before, after adk.ChatModelAgentState) error {
			rec := recorderFrom(cbCtx)
			if rec == nil {
				return nil
			}
			rec.addCompact(CompactInfo{
				MessagesBefore: len(before.Messages),
				MessagesAfter:  len(after.Messages),
				Summary:        lastAssistantText(after.Messages),
			})
			return nil
		},
	})
}

// lastAssistantText 取压缩后最后一条有内容的助手/摘要消息，给面板做预览。
func lastAssistantText(msgs []*schema.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m == nil || m.Content == "" {
			continue
		}
		// 官方 finalizer 产出的摘要消息未必是 assistant 角色（实现细节），
		// 所以这里不按角色过滤，只取最后一条有内容的。
		return truncateRunes(m.Content, 600)
	}
	return ""
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
