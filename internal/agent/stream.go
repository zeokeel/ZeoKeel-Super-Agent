package agent

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// streamingModel 在模型流上挂一个分叉，让面板能边生成边看到文本。
//
// 为什么需要它：eino 的 ReAct（以及 ADK 的 tool-call 分支）必须先把整轮模型输出
// 读完才能判断"这是最终答案还是工具调用"，官方事件流因此会在整轮结束后才一次性
// 吐出文本。对桌面面板来说那是"卡住很久然后啪一下出现全文"。这里在模型层分一路
// 旁路，只做展示，不改变主链路的判断；文本事件的顺序由 recorder 的序号保证。
type streamingModel struct {
	inner model.ToolCallingChatModel
	rec   *recorder
}

func (m *streamingModel) Generate(ctx context.Context, input []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {
	return m.inner.Generate(ctx, input, opts...)
}

func (m *streamingModel) Stream(ctx context.Context, input []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := m.inner.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	copies := sr.Copy(2)
	m.rec.streamWG.Add(1)
	go func() {
		defer m.rec.streamWG.Done()
		defer copies[0].Close()
		for {
			chunk, recvErr := copies[0].Recv()
			if recvErr != nil {
				return
			}
			if chunk != nil {
				m.rec.addText(chunk.Content)
			}
		}
	}()
	// 主链路必须读到带工具调用的完整分片，所以返回另一份拷贝。
	return copies[1], nil
}

// WithTools 必须返回同样包装过的实例：ADK/ReAct 内部正是用 WithTools 派生出真正
// 被调用的模型，直接透传会让旁路丢失。
func (m *streamingModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &streamingModel{inner: inner, rec: m.rec}, nil
}

func newStreamingModel(mb model.ToolCallingChatModel, rec *recorder) model.ToolCallingChatModel {
	return &streamingModel{inner: mb, rec: rec}
}

var _ model.ToolCallingChatModel = (*streamingModel)(nil)
