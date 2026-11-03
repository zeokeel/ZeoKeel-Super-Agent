package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// bigOutputTool 返回一大段输出，用来验证超限结果会被落盘并替换成路径提示。
func bigOutputTool(blob string) tool.BaseTool {
	t, err := utils.InferTool("big_output", "Return a large blob of text.",
		func(_ context.Context, _ struct{}) (string, error) {
			return blob, nil
		})
	if err != nil {
		panic(err)
	}
	return t
}

// 工具结果超限时必须落盘，回给模型的是"预览 + 文件路径"，而且文件真的在工作区里，
// 模型可以用 read 工具把它读回来（design §5.4）。
func TestToolResultTruncatedAndOffloaded(t *testing.T) {
	srv := startMock(t)
	blob := strings.Repeat("ZeoKeel 的大输出用来测试截断。", 400) // 远超上限
	srv.ToolName = "big_output"
	srv.ToolArgs = "{}"

	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-tool", func(c *Config) {
		c.ToolOutputLimit = 512
		c.ExtraTools = []tool.BaseTool{bigOutputTool(blob)}
	}))

	var toolOutput string
	res, err := r.RunSession(context.Background(), RunRequest{System: "sys", Task: "跑一下 big_output"}, func(e Event) {
		if e.Kind == EventToolEnd && e.Tool == "big_output" {
			toolOutput = e.Output
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	// 回调看到的是工具原始输出（含完整内容），截断发生在中间件层，不改变"实际跑了什么"。
	if !strings.Contains(toolOutput, "ZeoKeel") {
		t.Fatalf("tool end event lost its output: %q", toolOutput)
	}

	// 模型看到的是持久化提示，而不是整段输出。
	last := srv.LastRequest()
	var modelSaw string
	for _, m := range last.Messages {
		if m.Role == "tool" {
			modelSaw = m.Content
		}
	}
	if !strings.Contains(modelSaw, "persisted-output") {
		t.Fatalf("the model was not given a persisted-output notice: %q", modelSaw)
	}
	if !strings.Contains(modelSaw, "read") {
		t.Fatalf("the persisted-output notice must name the read tool: %q", modelSaw)
	}
	if len(modelSaw) > len(blob)/2 {
		t.Fatalf("the model still saw the huge output (%d bytes)", len(modelSaw))
	}
	// 最终答案会回显工具结果，说明模型拿到的确实是截断后的版本。
	if !strings.Contains(res.Answer, "persisted-output") {
		t.Fatalf("final answer did not echo the truncated result: %q", res.Answer)
	}

	// 完整输出必须真的落盘，供模型和面板打开。
	files, err := filepath.Glob(filepath.Join(dir, ".zeokeel", "tool-output", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no offloaded tool output file was written")
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < len(blob)/2 {
		t.Fatalf("offloaded file is too small: %d bytes", len(data))
	}
}

// 小结果不应该被落盘：截断只处理超限的那部分。
func TestSmallToolResultNotOffloaded(t *testing.T) {
	srv := startMock(t)
	srv.ToolName = "big_output"
	srv.ToolArgs = "{}"
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-tool", func(c *Config) {
		c.ExtraTools = []tool.BaseTool{bigOutputTool("small")}
	}))
	if _, err := r.RunSession(context.Background(), RunRequest{System: "sys", Task: "hi"}, nil); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".zeokeel", "tool-output", "*.txt"))
	if len(files) != 0 {
		t.Fatalf("small tool result was offloaded: %v", files)
	}
}
