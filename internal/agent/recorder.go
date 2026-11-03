package agent

import (
	"context"
	"errors"
	"io"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	ub "github.com/cloudwego/eino/utils/callbacks"
)

// recorderContextKey 让 adk 中间件（压缩回调）能拿到本次运行的 recorder。
// 中间件只收到 ctx，没有别的通道把"压缩发生了"告诉面板。
type recorderContextKey struct{}

func withRecorder(ctx context.Context, rec *recorder) context.Context {
	return context.WithValue(ctx, recorderContextKey{}, rec)
}

func recorderFrom(ctx context.Context) *recorder {
	rec, _ := ctx.Value(recorderContextKey{}).(*recorder)
	return rec
}

// callbackHandler 把 eino 核心回调翻译成面板事件：
//   - 工具回调：开始/结束（带参数与耗时），这是"它跑了什么"的唯一来源；
//   - 模型回调：token 用量，供面板显示本 Ticket 已用多少（design §5.4）。
//
// 用量从 callbacks 取而不是从消息的 ResponseMeta 取，是因为流式响应的用量在最后
// 一个 chunk 上，回调的 OnEndWithStreamOutput 正好能拿到，而且摘要这类非流式
// 调用同样会走 OnEnd，两条路都覆盖。
func callbackHandler(rec *recorder) callbacks.Handler {
	return ub.NewHandlerHelper().
		Tool(&ub.ToolCallbackHandler{
			OnStart: func(ctx context.Context, info *callbacks.RunInfo, in *tool.CallbackInput) context.Context {
				rec.toolStarted(info.Name, in.ArgumentsInJSON, compose.GetToolCallID(ctx))
				return ctx
			},
			OnEnd: func(ctx context.Context, info *callbacks.RunInfo, out *tool.CallbackOutput) context.Context {
				rec.toolEnded(info.Name, out.Response, compose.GetToolCallID(ctx), nil)
				return ctx
			},
			OnEndWithStreamOutput: func(ctx context.Context, info *callbacks.RunInfo,
				out *schema.StreamReader[*tool.CallbackOutput]) context.Context {
				// 流式工具的输出必须读完，否则生产侧的 pipe 会阻塞。
				defer out.Close()
				var joined []byte
				for {
					chunk, err := out.Recv()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						rec.toolEnded(info.Name, string(joined), compose.GetToolCallID(ctx), err)
						return ctx
					}
					if chunk != nil {
						joined = append(joined, chunk.Response...)
					}
				}
				rec.toolEnded(info.Name, string(joined), compose.GetToolCallID(ctx), nil)
				return ctx
			},
			OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
				rec.toolEnded(info.Name, "", compose.GetToolCallID(ctx), err)
				return ctx
			},
		}).
		ChatModel(&ub.ModelCallbackHandler{
			OnEnd: func(ctx context.Context, info *callbacks.RunInfo, out *model.CallbackOutput) context.Context {
				rec.addUsage(out.TokenUsage)
				return ctx
			},
			OnEndWithStreamOutput: func(ctx context.Context, info *callbacks.RunInfo,
				out *schema.StreamReader[*model.CallbackOutput]) context.Context {
				// 必须在后台协程里读：这个回调是在模型 Stream() 返回前被同步调用的，
				// 同步读完等于把整轮模型输出堵在回调里，面板就看不到逐字输出了。
				// recorder 会等到它读完再发 done，所以事件顺序仍然完整。
				rec.usageWG.Add(1)
				go func() {
					defer rec.usageWG.Done()
					defer out.Close()
					var last *model.CallbackOutput
					for {
						chunk, err := out.Recv()
						if err != nil {
							break
						}
						if chunk != nil && chunk.TokenUsage != nil {
							last = chunk
						}
					}
					if last != nil {
						rec.addUsage(last.TokenUsage)
					}
				}()
				return ctx
			},
			OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
				return ctx
			},
		}).
		Handler()
}

// toolKey 用 callID 关联同一工具的 start/end；没有 callID（少数非工具节点）时退回名字。
func toolKey(name, callID string) string {
	if callID != "" {
		return callID
	}
	return name
}

func (r *recorder) toolStarted(name, args, callID string) {
	key := toolKey(name, callID)
	r.mu.Lock()
	r.toolStart[key] = toolStartInfo{name: name, args: args, start: nowFunc()}
	r.mu.Unlock()
	r.send(Event{Kind: EventToolStart, Tool: name, Args: args})
}

func (r *recorder) toolEnded(name, output, callID string, err error) {
	key := toolKey(name, callID)
	r.mu.Lock()
	info, ok := r.toolStart[key]
	if ok {
		delete(r.toolStart, key)
	} else {
		// 没有 start 也要能收尾，否则 Duration 会变成"从零值时间到现在"的荒谬值。
		info = toolStartInfo{name: name, start: nowFunc()}
	}
	r.mu.Unlock()
	if err != nil {
		r.send(Event{Kind: EventError, Tool: name, Args: info.args, Err: err})
		return
	}
	r.send(Event{Kind: EventToolEnd, Tool: name, Args: info.args, Output: output,
		Duration: nowFunc().Sub(info.start)})
}
