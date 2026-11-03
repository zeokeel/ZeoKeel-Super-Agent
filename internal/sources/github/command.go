package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Command 执行一次 gh CLI 调用。Source 不知道 gh 怎么被找到、怎么认证、有没有
// 企业域名；测试塞一个 fake 就完全不碰网络和 gh。
//
// 故意只有一个方法：fake 只需返回固定 JSON，写测试的成本接近零。
type Command interface {
	// Run 返回 gh 的 stdout。失败时必须返回 error，且若 gh 报了 HTTP 状态码，
	// 该错误应能被 AsCLIError 识别（见 CLIError）。
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// CLIError 是一次失败的 gh 调用中我们真正需要的部分：HTTP 状态码。
// 410 Gone / 403 locked 决定一条回复要不要永久放弃（§7.2 的 gone 语义），
// 所以状态码不能埋在自由文本里。
type CLIError struct {
	Args   []string
	Code   int    // gh 报告的 HTTP 状态码；没报告时为 0
	Stderr string // 原始 stderr，日志与面板需要
	Err    error
}

// Error implements error.
func (e *CLIError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if e.Code != 0 {
		return fmt.Sprintf("github: gh %s failed (HTTP %d): %s", strings.Join(e.Args, " "), e.Code, msg)
	}
	return fmt.Sprintf("github: gh %s failed: %s", strings.Join(e.Args, " "), msg)
}

// Unwrap 让 errors.Is(err, context.Canceled) 之类的判断仍然成立。
func (e *CLIError) Unwrap() error { return e.Err }

var httpCodePattern = regexp.MustCompile(`(?i)\bHTTP[ /]?(\d{3})\b`)

// httpCode 从 gh 的 stderr 里取出 HTTP 状态码；没有就返回 0。
// gh api 失败时通常形如 "gh: Not Found (HTTP 404)"。
func httpCode(stderr string) int {
	m := httpCodePattern.FindStringSubmatch(stderr)
	if m == nil {
		return 0
	}
	code := 0
	for _, r := range m[1] {
		code = code*10 + int(r-'0')
	}
	return code
}

// AsCLIError 把任意错误还原成 *CLIError，供 Channel 判断 gone。
func AsCLIError(err error) (*CLIError, bool) {
	var cliErr *CLIError
	if errors.As(err, &cliErr) {
		return cliErr, true
	}
	return nil, false
}

// ExecCommand 是生产用的 Command：真的起一个 gh 进程。只用 os/exec，零 cgo。
type ExecCommand struct {
	// Path 是 gh 可执行文件；空表示 "gh"（从 PATH 找）。
	Path string
}

// RunWithHeaders 实现 HeaderCommand：跑 `gh api -i ...`，把 gh 打到 stdout 的
// "状态行 + 响应头 + body" 解析成结构化结果。
//
// 关键点：gh 在 HTTP 错误（含 304）时仍会把响应头/回应体写到 stdout、只把
// 错误写 stderr 并以非零退出；所以这里即使 cmd.Run 失败也要先解析 stdout，
// 再返回带状态码的 CLIError。这样 ETag/304 判断不依赖 stderr 文本（N-14）。
func (c ExecCommand) RunWithHeaders(ctx context.Context, args ...string) ([]byte, int, http.Header, error) {
	path := c.Path
	if path == "" {
		path = "gh"
	}
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	body, status, header := splitIncludeOutput(stdout.Bytes())
	if runErr != nil {
		if status == 0 {
			status = httpCode(stderr.String())
		}
		return body, status, header, &CLIError{Args: args, Code: status, Stderr: stderr.String(), Err: runErr}
	}
	return body, status, header, nil
}

// splitIncludeOutput 解析 `gh api -i` 的输出：第一段是 HTTP 状态行 + 头，
// 空行之后是 body。解析失败（没有头）时把整段当 body，状态码 0。
//
// 刻意不用 http.ReadResponse：它会对带 `Transfer-Encoding: chunked` 头的响应
// 做分块解码，而 gh 打印出来的 body 已经是解码后的明文——照头解会把 body 吃掉。
// 这里只做"切头 + 逐行解析 header"，body 原样返回。
func splitIncludeOutput(raw []byte) ([]byte, int, http.Header) {
	idx := bytes.Index(raw, []byte("\r\n\r\n"))
	sepLen := 4
	if idx < 0 {
		// 有些环境只输出 LF 空行。
		idx = bytes.Index(raw, []byte("\n\n"))
		sepLen = 2
	}
	if idx < 0 {
		return raw, 0, nil
	}
	head := strings.Split(string(raw[:idx]), "\n")
	body := raw[idx+sepLen:]
	status := 0
	if len(head) > 0 {
		if fields := strings.Fields(strings.TrimSpace(head[0])); len(fields) >= 2 {
			if code, err := strconv.Atoi(fields[1]); err == nil {
				status = code
			}
		}
	}
	header := http.Header{}
	for _, line := range head[1:] {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		header.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}
	return body, status, header
}

// Run implements Command.
func (c ExecCommand) Run(ctx context.Context, args ...string) ([]byte, error) {
	path := c.Path
	if path == "" {
		path = "gh"
	}
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, &CLIError{Args: args, Code: httpCode(stderr.String()), Stderr: stderr.String(), Err: err}
	}
	return stdout.Bytes(), nil
}
