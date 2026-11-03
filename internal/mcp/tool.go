package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxToolNameLen 是工具名上限。OpenAI 兼容端点普遍限制 64 字符，超了会直接
// 400 或静默截断（截断后撞名更糟），所以在这里主动裁并加哈希保唯一。
const maxToolNameLen = 64

// toolEntry 是一个 MCP 工具在发现阶段算好的静态信息。
type toolEntry struct {
	rawName string // MCP server 里的原名，调用时必须用它
	name    string // 加前缀后的名字，模型看到的是这个
	desc    string
	info    *schema.ToolInfo
}

func newToolEntry(serverName string, t *mcpsdk.Tool) (*toolEntry, error) {
	if t == nil {
		return nil, fmt.Errorf("mcp: server %q 返回了空工具定义", serverName)
	}
	name, err := buildToolName(serverName, t.Name)
	if err != nil {
		return nil, fmt.Errorf("mcp: server %q: %w", serverName, err)
	}
	desc := strings.TrimSpace(t.Description)
	if desc == "" {
		// 没描述的工具模型基本不会用；给一句兜底至少能让它知道来源。
		desc = fmt.Sprintf("MCP tool %q from server %q.", t.Name, serverName)
	}
	info := &schema.ToolInfo{Name: name, Desc: desc}
	if t.InputSchema != nil {
		raw, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("mcp: 工具 %q 的 inputSchema 无法序列化: %w", t.Name, err)
		}
		var js jsonschema.Schema
		if err := json.Unmarshal(raw, &js); err != nil {
			return nil, fmt.Errorf("mcp: 工具 %q 的 inputSchema 不是合法 JSON Schema: %w", t.Name, err)
		}
		info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&js)
	}
	return &toolEntry{rawName: t.Name, name: name, desc: desc, info: info}, nil
}

// buildToolName 生成 mcp__<server>__<tool>，并按 provider 的长度限制收敛。
func buildToolName(server, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("工具名为空")
	}
	s := sanitizeName(server)
	if s == "" {
		s = "server"
	}
	t := sanitizeName(raw)
	if t == "" {
		t = "tool"
	}
	name := "mcp__" + s + "__" + t
	if len(name) <= maxToolNameLen {
		return name, nil
	}
	// 截断会让长名字互相撞车，所以把完整名字的哈希钉在尾部：名字变短但唯一性不变。
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	suffix := fmt.Sprintf("-%08x", h.Sum32())
	return name[:maxToolNameLen-len(suffix)] + suffix, nil
}

// sanitizeName 把任意字符串收敛成工具名允许的字符集。
// 只做替换不做删除，保持长度可预测，也避免两个不同 server 名收敛成同一个空串。
func sanitizeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// serverTool 把一次 MCP 工具调用包装成 eino 的 InvokableTool。
//
// 只实现 InvokableTool（不实现 StreamableTool）：MCP 的进度通知是独立通道，
// 硬塞成流式返回值会让"部分结果"和"最终结果"混在一起，反而更容易出错。
type serverTool struct {
	conn  *serverConn
	entry *toolEntry
	cfg   Config
}

// Info 返回构造时算好的工具描述，不做 I/O。
func (t *serverTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.entry.info, nil
}

// InvokableRun 执行一次 MCP 工具调用。
//
// 关键决定：工具级错误（result.IsError）返回 nil error，把错误文本作为结果交给
// 模型。eino 的 ToolsNode 会把 Go error 当成整轮失败向上冒，一个"文件不存在"
// 就足以终止整个 Ticket；而模型看到错误文本后通常能自己换路径重试。
// 传输层失败才返回 error（并且已经按可重试/永久分类）。
func (t *serverTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	text, isError, err := t.conn.call(ctx, t.entry.rawName, argumentsInJSON)
	if err != nil {
		return "", err
	}
	if isError {
		text = "MCP 工具返回错误（isError=true）：\n" + text
	}
	return t.limit(text), nil
}

// limit 按配置裁剪超长结果。
//
// 截断本身失败（例如落盘目录不可写）不能把工具调用变成失败：结果仍然可用，
// 只是少了一条"完整结果在哪"的提示，所以 limitResult 不返回 error。
func (t *serverTool) limit(text string) string {
	if t.cfg.SpillDir == "" {
		return limitResult(text, t.cfg.MaxResultLines, t.cfg.MaxResultBytes, "", "", "")
	}
	return limitResult(text, t.cfg.MaxResultLines, t.cfg.MaxResultBytes,
		t.cfg.SpillDir, t.conn.cfg.Name, t.entry.rawName)
}
