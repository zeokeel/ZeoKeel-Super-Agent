package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// decodeArgs 把模型给的工具参数解成 map。
//
// 空字符串与 "null" 都当作"无参数"：不同模型在无参调用上的行为不一致，
// 有的发 ""，有的发 "{}"，有的发 "null"，这里统一收敛，避免无参工具随机失败。
func decodeArgs(argumentsInJSON string) (any, error) {
	s := strings.TrimSpace(argumentsInJSON)
	if s == "" || s == "null" {
		return nil, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(s), &args); err != nil {
		return nil, fmt.Errorf("mcp: 工具参数不是合法 JSON 对象: %w", err)
	}
	return args, nil
}

// flattenResult 把 MCP 的多段 Content 压成一段文本。
//
// 图片/音频不展开成 base64：那会把上下文预算瞬间吃光（design §5.4），
// 只留一个占位描述，让模型知道"这里有个非文本产物"。
func flattenResult(res *mcpsdk.CallToolResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range res.Content {
		switch v := c.(type) {
		case *mcpsdk.TextContent:
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(v.Text)
		case *mcpsdk.ImageContent:
			fmt.Fprintf(&b, "[图片 %s，%d 字节 base64 已省略]", v.MIMEType, len(v.Data))
		case *mcpsdk.AudioContent:
			fmt.Fprintf(&b, "[音频 %s，%d 字节 base64 已省略]", v.MIMEType, len(v.Data))
		case *mcpsdk.ResourceLink:
			fmt.Fprintf(&b, "[资源链接 %s]", v.URI)
		case *mcpsdk.EmbeddedResource:
			b.WriteString("[内嵌资源已省略]")
		default:
			// 未来新增的 Content 类型不丢信息：序列化出来总比静默吞掉好。
			if j, err := json.Marshal(c); err == nil {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.Write(j)
			}
		}
	}
	if b.Len() == 0 && res.StructuredContent != nil {
		if j, err := json.Marshal(res.StructuredContent); err == nil {
			b.Write(j)
		}
	}
	return b.String()
}

// limitResult 按"前 N 行 + 字节上限"裁剪工具结果，并在需要时落盘全文。
//
// 刻意不返回 error：截断是保护措施，落盘失败（目录只读等）不该把一次成功的
// 工具调用变成失败。失败信息会写进返回文本，模型和人都能看到。
func limitResult(text string, maxLines, maxBytes int, spillDir, server, tool string) string {
	totalLines := countLines(text)
	if totalLines <= maxLines && len(text) <= maxBytes {
		return text
	}

	head := text
	if len(head) > maxBytes {
		head = head[:maxBytes]
		// 别把一个 UTF-8 字符劈成两半，否则模型会收到乱码。
		for len(head) > 0 && !utf8.ValidString(head) {
			head = head[:len(head)-1]
		}
		// 尽量在行边界收尾，半行 JSON/代码比整行截断更难读。
		if i := strings.LastIndexByte(head, '\n'); i > 0 {
			head = head[:i]
		}
	}
	if lines := strings.Split(head, "\n"); len(lines) > maxLines {
		head = strings.Join(lines[:maxLines], "\n")
	}
	kept := countLines(head)

	var b strings.Builder
	b.WriteString(head)
	fmt.Fprintf(&b, "\n…（结果已截断：原 %d 行 / %d 字节，保留前 %d 行 / %d 字节）",
		totalLines, len(text), kept, len(head))

	if spillDir != "" {
		if path, err := spill(spillDir, server, tool, text); err == nil {
			fmt.Fprintf(&b, "\n完整输出已落盘：%s\n（用 read 工具按 offset/limit 查看）", path)
		} else {
			b.WriteString("\n（完整结果落盘失败，如需全部内容请让工具分页返回）")
		}
	}
	return b.String()
}

// spill 把完整结果写到 spills 目录，返回绝对路径。
//
// 文件名带纳秒时间戳与随机后缀：同名工具并发调用时不能互相覆盖，
// 否则模型拿到的"完整结果"其实是另一次调用的输出。
func spill(dir, server, tool, content string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s-%d-%s.txt",
		fileComponent(server), fileComponent(tool), time.Now().UnixNano(), randSuffix())
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	return abs, nil
}

// fileComponent 把 server/tool 名收敛成安全文件名片段。
func fileComponent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 40 {
			break
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	return b.String()
}

func randSuffix() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano()%1e6)
	}
	return hex.EncodeToString(buf[:])
}

// countLines 把"行数"定义得和编辑器一致：末尾换行不算多一行。
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n") + 1
	if strings.HasSuffix(s, "\n") {
		n--
	}
	return n
}
