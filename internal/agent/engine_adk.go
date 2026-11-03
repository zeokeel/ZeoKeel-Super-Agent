package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// runADK 是 Worker 的正式运行路径：eino ADK 的 ChatModelAgent + Runner，
// 会话事件由官方 FileStore 落盘；压缩与工具结果截断是官方中间件。
func (r *Runner) runADK(ctx context.Context, req RunRequest, emit func(Event), resumed bool) (RunResult, error) {
	start := time.Now()
	rec := newRecorder(r.cfg, req.SessionID, emit)
	rec.compactSink = r.compactSink(req.SessionID)
	if resumed {
		rec.send(Event{Kind: EventResume, Text: req.Task})
	}

	mb, err := r.newChatModel(ctx)
	if err != nil {
		rec.send(Event{Kind: EventError, Err: err})
		return rec.result(req.SessionID, resumed), err
	}
	ag, err := r.newADKAgent(ctx, mb, rec, req.System)
	if err != nil {
		rec.send(Event{Kind: EventError, Err: err})
		return rec.result(req.SessionID, resumed), err
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           ag,
		EnableStreaming: true,
		SessionID:       req.SessionID,
		SessionStore:    r.store,
	})
	// 中间件（压缩回调）从 ctx 里取 recorder；工具/模型回调也从这里拿到同一份。
	runCtx := withRecorder(ctx, rec)
	runCtx = callbacks.InitCallbacks(runCtx, &callbacks.RunInfo{Name: "zeokeel-worker"}, callbackHandler(rec))

	iter := runner.Run(runCtx, []*schema.Message{schema.UserMessage(req.Task)})
	runErr := consumeADKEvents(runCtx, iter, rec)
	// 无论成功还是被取消，都先把旁路文本/用量发完再收尾，避免事件跑到返回值之后。
	rec.waitStreams()
	if runErr != nil {
		if ctx.Err() != nil {
			// 取消返回后调用方可能立刻再跑 RunSession；等 adk 把收尾事件写完，日志才是
			// 一致的状态，否则"停止→继续"会读到半截会话。
			r.waitSessionSettled(req.SessionID)
		}
		return rec.result(req.SessionID, resumed), runErr
	}
	rec.done(start)
	return rec.result(req.SessionID, resumed), nil
}

// compactSink 把压缩事件也落进会话日志：事件日志是面板与恢复的唯一事实来源，
// 只在内存里发一个事件的话，重启后面板就不知道"这段历史被摘要过"。
func (r *Runner) compactSink(sessionID string) func(CompactInfo) {
	return func(info CompactInfo) {
		eventID, err := newSessionEventID()
		if err != nil {
			return
		}
		_ = r.store.AppendEvents(context.Background(), sessionID, []*adk.SessionEvent[*schema.Message]{{
			EventID:   eventID,
			Timestamp: nowFunc().UTC(),
			Kind:      compactEventKind,
			Extension: &adk.SessionExtensionEvent{},
			Extra: map[string]any{
				"messages_before": info.MessagesBefore,
				"messages_after":  info.MessagesAfter,
				"summary":         info.Summary,
			},
		}})
	}
}

// newADKAgent 组装 Worker 的 agent：工具 + 两条官方中间件。
//
// 中间件顺序不重要（一个改消息、一个改工具结果），但都必须在构造时给进去：
// Headers 一旦 Run 开始就不能改。
func (r *Runner) newADKAgent(ctx context.Context, mb model.ToolCallingChatModel, rec *recorder,
	system string) (adk.Agent, error) {
	summarizer, err := r.newSummarizer(ctx, mb)
	if err != nil {
		return nil, err
	}
	reducer, err := r.newToolResultReducer(ctx)
	if err != nil {
		return nil, err
	}
	subHandler, err := r.newSubagents(ctx, mb)
	if err != nil {
		return nil, err
	}
	fileMiddleware, err := newFilesystemMiddleware(ctx, r.cfg.Workdir, nil)
	if err != nil {
		return nil, fmt.Errorf("agent: filesystem middleware: %w", err)
	}
	jobsMiddleware, err := r.newJobsMiddleware(ctx)
	if err != nil {
		return nil, err
	}
	// 悬空 tool_call 修补用 eino 官方中间件：它每次模型调用前扫历史，给"有调用、
	// 没结果"的 tool_call 补一条占位 tool 消息。这覆盖的不只是崩溃恢复，也包括
	// 工具执行中途被取消、并行工具丢结果等所有会让真实 provider 400 的历史形态
	// （design §5.8）。RunSession 仍会在恢复时先落盘补丁，两者互补：落盘补丁让
	// 面板/统计看得见中断，中间件兜住运行中出现的历史不一致。
	patcher, err := patchtoolcalls.New(ctx, &patchtoolcalls.Config{
		MarkSynthetic: true,
		PatchedToolResultGenerator: func(_ context.Context, _, _ string,
			_ *schema.ToolArgument) (*patchtoolcalls.PatchedToolResult, error) {
			return &patchtoolcalls.PatchedToolResult{Content: interruptedToolNote}, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("agent: patchtoolcalls middleware: %w", err)
	}
	ag, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "zeokeel-worker",
		Description: "ZeoKeel Worker：在自己的工作区里完成一个 assignment",
		Instruction: system,
		// agent 用旁路模型，摘要调用用原模型（Generate 不需要逐字展示）。
		Model: newStreamingModel(mb, rec),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: r.tools},
		},
		MaxIterations: maxSteps(r.cfg.MaxSteps),
		Handlers: append([]adk.ChatModelAgentMiddleware{
			summarizer, reducer, patcher, fileMiddleware, jobsMiddleware, newToolRefusalMiddleware(),
		}, subHandler...),
	})
	if err != nil {
		return nil, fmt.Errorf("agent: new adk agent: %w", err)
	}
	return ag, nil
}

// reportRunError 区分"被取消"和"真失败"：前者是可恢复的中断（会话日志还在，
// 恢复能接着跑），后者是错误。两者都先把事件发给面板。
func reportRunError(ctx context.Context, rec *recorder, err error) error {
	cause := ctx.Err()
	if cause != nil {
		rec.send(Event{Kind: EventCanceled, Err: cause})
		return fmt.Errorf("agent: run canceled: %w", cause)
	}
	rec.send(Event{Kind: EventError, Err: err})
	return err
}

// consumeADKEvents 把 ADK 的事件流翻译成面板事件。
//
// 工具事件不走这里：ADK 的工具结果事件没有参数，而回调里有参数与耗时，所以工具
// 的 start/end 统一由 callbackHandler 上报，这里只负责助手文本与收尾。
func consumeADKEvents(ctx context.Context, iter *adk.AsyncIterator[*adk.AgentEvent], rec *recorder) error {
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev == nil {
			continue
		}
		if ev.Err != nil {
			return reportRunError(ctx, rec, ev.Err)
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		mv := ev.Output.MessageOutput
		if mv.IsStreaming {
			// 流必须读完再丢弃：ADK 的流是 pipe，未消费会让生产侧协程挂住。
			// 助手文本已由 streamingModel 的旁路逐字发出，这里不再重复上报。
			if mv.Role == schema.Assistant {
				if err := drainAssistantStream(mv.MessageStream); err != nil {
					return reportRunError(ctx, rec, err)
				}
			} else {
				closeStream(mv.MessageStream)
			}
			continue
		}
		if mv.Role == schema.Assistant && mv.Message != nil {
			rec.addText(mv.Message.Content)
		}
	}
	if ctx.Err() != nil {
		return reportRunError(ctx, rec, ctx.Err())
	}
	return nil
}

// drainAssistantStream 只负责读完流；文本展示由 streamingModel 旁路负责。
func drainAssistantStream(sr *schema.StreamReader[*schema.Message]) error {
	defer sr.Close()
	for {
		_, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func closeStream(sr *schema.StreamReader[*schema.Message]) {
	if sr == nil {
		return
	}
	defer sr.Close()
	for {
		if _, err := sr.Recv(); err != nil {
			return
		}
	}
}

// waitSessionSettled 等 adk 把取消/中断后的收尾事件写完（有上限）。
//
// 取消是异步收尾的：Run 返回时状态事件可能还在写。让"停止后马上继续"可预测，
// 比节省几毫秒更重要；上限保证真崩了也不会把调用方卡住。
func (r *Runner) waitSessionSettled(sessionID string) {
	deadline := nowFunc().Add(time.Second)
	for nowFunc().Before(deadline) {
		events, err := r.loadEvents(context.Background(), sessionID)
		if err == nil && sessionIsIdle(events) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sessionIsIdle(events []*adk.SessionEvent[*schema.Message]) bool {
	idle := false
	for _, ev := range events {
		if ev != nil && ev.Lifecycle != nil {
			idle = ev.Lifecycle.State == adk.SessionRunStateIdle
		}
	}
	return idle
}

// maxSteps bounds one run. Zero would mean "no bound" to some engines, and a model
// that keeps calling tools would then never finish — a 10-hour task is acceptable,
// an unbounded loop is not (design §5.6).
func maxSteps(v int) int {
	if v > 0 {
		return v
	}
	return 40
}
