package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
)

// toolOutputDirRel 是落盘工具输出的工作区相对目录。放在工作区内有两个好处：
// 模型可以用 read 工具打开它，人也能在面板"打开目录"里直接看到（design §5.4）。
const toolOutputDirRel = ".zeokeel/tool-output"

// toolOutputBackend 实现 reduction.Backend：把官方中间件要落盘的内容写进工作区。
//
// 中间件只负责"什么时候截断、截断后给模型什么提示"，存哪里由我们决定；写盘走
// os.Root，因此中间件传进来的相对路径即使带 .. 或借符号链接也出不去（F3：旧实现
// 复用的 inside() 只是词法检查）。
type toolOutputBackend struct {
	root string
}

func (b *toolOutputBackend) Write(_ context.Context, req *filesystem.WriteRequest) error {
	if req == nil {
		return fmt.Errorf("agent: tool output: nil write request")
	}
	backend := &localBackend{root: b.root}
	return backend.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: req.FilePath,
		Content:  req.Content,
	})
}

// newToolResultReducer 用官方 reduction 中间件做工具结果截断。
//
// 只开截断、不开清理：清空历史工具结果会把"我已经跑过什么"变成不可见，而历史压缩
// 已经由 summarization 负责；两件事都做等于丢两次信息。
//
// ReadFileToolName 必须与我们真实的 read 工具同名，否则提示里的"用 read 查看"会
// 指向一个模型没有的工具，它就会开始乱猜。
func (r *Runner) newToolResultReducer(_ context.Context) (adk.ChatModelAgentMiddleware, error) {
	return reduction.New(context.Background(), &reduction.Config{
		Backend:           &toolOutputBackend{root: r.cfg.Workdir},
		SkipClear:         true,
		ReadFileToolName:  "read",
		MaxLengthForTrunc: r.cfg.ToolOutputLimit,
		TruncExcludeTools: []string{"read", "edit", "write"},
		GenTruncOffloadFilePath: func(_ context.Context, detail *reduction.ToolDetail) (string, error) {
			id := "tool-result"
			if detail != nil && detail.ToolContext != nil && detail.ToolContext.CallID != "" {
				id = detail.ToolContext.CallID
			}
			return filepath.Join(toolOutputDirRel, sanitizeFileName(id)+".txt"), nil
		},
	})
}

// sanitizeFileName 把 tool call id 变成安全文件名：call id 来自模型，不能直接当路径用。
func sanitizeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 96 {
			break
		}
	}
	if b.Len() == 0 {
		return "tool-result"
	}
	return b.String()
}
