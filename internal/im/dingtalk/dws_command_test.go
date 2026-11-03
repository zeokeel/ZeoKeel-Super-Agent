package dingtalk

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
)

// 这些测试用临时 shell 脚本冒充 dws：验证 command 实现的参数拼装、JSON 解析、
// 错误分类与取消语义，不需要真的安装 DWS，也不会发起网络请求。
func requirePOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("dws command tests use POSIX shell scripts")
	}
}

func writeDWSCommand(t *testing.T, script string) string {
	t.Helper()
	requirePOSIX(t)
	path := filepath.Join(t.TempDir(), "dws")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake dws: %v", err)
	}
	return path
}

func readArgsFile(t *testing.T, path string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	parts := bytes.Split(raw, []byte{0})
	// 末尾 NUL 会产生一个空元素。
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// auth status：登录/未登录两种结果都能解析。
func TestCommandDWSClientAuthStatus(t *testing.T) {
	okJSON := `{"loggedIn":true,"account":"robot@corp","authUrl":""}`
	client := NewCommandDWSClient(DWSConfig{Command: writeDWSCommand(t, `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then echo '`+okJSON+`'; exit 0; fi
echo "unexpected: $*" >&2; exit 2
`)})
	st, err := client.AuthStatus(context.Background())
	if err != nil || !st.LoggedIn || st.Account != "robot@corp" {
		t.Fatalf("AuthStatus = %+v, %v", st, err)
	}

	authURL := "https://login.dingtalk.example/device"
	notLoggedIn := `{"loggedIn":false,"account":"","authUrl":"` + authURL + `"}`
	client2 := NewCommandDWSClient(DWSConfig{Command: writeDWSCommand(t, `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then echo '`+notLoggedIn+`'; exit 0; fi
exit 2
`)})
	st2, err := client2.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st2.LoggedIn || st2.AuthURL != authURL {
		t.Fatalf("AuthStatus = %+v", st2)
	}
}

// poll：解析群聊/单聊/mentions；坏 item 逐条跳过；空游标不带 --cursor。
func TestCommandDWSClientPollParsesAndSkipsBadItems(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "poll.args")
	payload := `{"cursor":"c9","messages":[` +
		`{"id":"m1","conversation":{"id":"cid_g","type":"group","name":"研发群"},` +
		`"sender":{"id":"u1","name":"张三","bot":false},"text":"@bot 看下",` +
		`"at":"2026-11-03T10:00:00+08:00","mentions":["bot"],"replyToBot":false},` +
		`{"id":"m2","conversation":{"id":"cid_d","type":"direct","name":""},` +
		`"sender":{"id":"u2","name":"李四","bot":true},"text":"部署完成",` +
		`"at":"2026-11-03T10:01:00Z","mentions":[],"replyToBot":true},` +
		`{"conversation":{"id":"cid_bad","type":"group"},"sender":{"id":"u3"},"text":"no id","at":"2026-11-03T10:02:00Z"},` +
		`{"id":"m4","conversation":{"id":"cid_bad","type":"group"},"sender":{"id":"u4"},"text":"bad time","at":"not-a-time"}` +
		`]}`
	cmdPath := writeDWSCommand(t, `#!/bin/sh
if [ "$1" = "poll" ]; then
  printf '%s\0' "$@" > `+argsFile+`
  echo '`+payload+`'
  exit 0
fi
echo "unexpected: $*" >&2; exit 2
`)
	client := NewCommandDWSClient(DWSConfig{Command: cmdPath, PollLimit: 50})
	page, err := client.Poll(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if page.Cursor != "c9" || len(page.Messages) != 2 || page.Skipped != 2 {
		t.Fatalf("PollPage = %+v (want 2 messages, 2 skipped, cursor c9)", page)
	}
	group := page.Messages[0]
	if group.ID != "m1" || group.ConversationType != "group" || group.SenderBot ||
		len(group.Mentions) != 1 || group.Mentions[0] != "bot" {
		t.Fatalf("group message = %+v", group)
	}
	direct := page.Messages[1]
	if direct.ConversationType != "direct" || !direct.SenderBot || !direct.ReplyToBot {
		t.Fatalf("direct message = %+v", direct)
	}
	args := readArgsFile(t, argsFile)
	if len(args) < 3 || string(args[0]) != "poll" || string(args[1]) != "--limit" || string(args[2]) != "50" {
		t.Fatalf("poll args = %q", args)
	}
	for _, arg := range args {
		if string(arg) == "--cursor" {
			t.Fatalf("first poll (empty cursor) must not pass --cursor: %q", args)
		}
	}

	// 有游标时必须原样传回 CLI。
	if _, err := client.Poll(context.Background(), "c9", 10); err != nil {
		t.Fatal(err)
	}
	args = readArgsFile(t, argsFile)
	joined := string(bytes.Join(args, []byte(" ")))
	if !strings.Contains(joined, "--cursor c9") || !strings.Contains(joined, "--limit 10") {
		t.Fatalf("poll args = %q, want --cursor c9 --limit 10", args)
	}
}

// send：marker + 幂等键通过参数传入；消息文本不经过 shell（命令替换不会执行）。
func TestCommandDWSClientSendMarkerAndNoShellInterpolation(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "send.args")
	sentinel := filepath.Join(dir, "pwned")
	cmdPath := writeDWSCommand(t, `#!/bin/sh
if [ "$1" = "send" ]; then
  printf '%s\0' "$@" > `+argsFile+`
  echo '{"messageId":"dw_1"}'
  exit 0
fi
exit 2
`)
	client := NewCommandDWSClient(DWSConfig{Command: cmdPath})
	// 如果经过 shell，$(touch ...) 与 ` + "`" + touch ...` + "`" + ` 会创建 sentinel。
	text := "hello; $(touch " + sentinel + ") `touch " + sentinel + "` \"quoted\""
	id, err := client.Send(context.Background(), "cid_1", text, "k-42")
	if err != nil || id != "dw_1" {
		t.Fatalf("Send = %q, %v", id, err)
	}
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("message text was shell-interpolated (sentinel exists: %v)", err)
	}
	args := readArgsFile(t, argsFile)
	if len(args) < 7 || string(args[0]) != "send" || string(args[1]) != "--conversation" || string(args[2]) != "cid_1" {
		t.Fatalf("send args = %q", args)
	}
	wantText := text + markerForDWS("k-42")
	if string(args[4]) != wantText {
		t.Fatalf("--text = %q, want %q", args[4], wantText)
	}
	if string(args[5]) != "--idempotency-key" || string(args[6]) != "k-42" {
		t.Fatalf("idempotency args = %q", args[5:])
	}
	if !strings.Contains(wantText, "<!-- zeokeel:k-42 -->") {
		t.Fatalf("marker missing from text: %q", wantText)
	}
	// 参数校验失败按永久错误，且不调用 CLI。
	if _, err := client.Send(context.Background(), "", "hi", "k"); err == nil || !IsPermanent(err) {
		t.Fatalf("empty conversation err = %v, want permanent", err)
	}
	if _, err := client.Send(context.Background(), "cid", "hi", ""); err == nil || !IsPermanent(err) {
		t.Fatalf("empty key err = %v, want permanent", err)
	}
}

// 4xx 永久错误 / 5xx 可重试：状态码从 stderr 文本里抓到。
func TestCommandDWSClientSendErrorClassification(t *testing.T) {
	mk := func(stderr string, exitCode int) DWSClient {
		return NewCommandDWSClient(DWSConfig{Command: writeDWSCommand(t, `#!/bin/sh
echo '`+stderr+`' >&2
exit `+strconv.Itoa(exitCode)+`
`)})
	}
	_, err := mk("HTTP 404 Not Found", 1).Send(context.Background(), "cid", "hi", "k")
	var de *DWSError
	if !errors.As(err, &de) || de.Status != 404 || !de.Permanent || !IsPermanent(err) {
		t.Fatalf("404 err = %v (want permanent HTTP 404)", err)
	}
	_, err = mk("gateway error: HTTP 503 Service Unavailable", 1).Send(context.Background(), "cid", "hi", "k")
	if !errors.As(err, &de) || de.Status != 503 || de.Permanent || IsPermanent(err) {
		t.Fatalf("503 err = %v (want retryable HTTP 503)", err)
	}
	if temp, ok := err.(interface{ Temporary() bool }); !ok || !temp.Temporary() {
		t.Fatalf("503 must be Temporary: %v", err)
	}
}

// 命令缺失：永久错误 + 可操作 detail（"安装 dws"）。
func TestCommandDWSClientMissingExecutable(t *testing.T) {
	client := NewCommandDWSClient(DWSConfig{Command: filepath.Join(t.TempDir(), "no-such-dws")})
	_, err := client.AuthStatus(context.Background())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("err = %v, want a permanent command error", err)
	}
	// 可操作 detail：中文"未安装"与英文 not executable 至少一个，且不可重试。
	if msg := err.Error(); !strings.Contains(msg, "未安装") && !strings.Contains(strings.ToLower(msg), "not executable") {
		t.Fatalf("detail = %q, want an actionable install/executable message", msg)
	}
	if temp, ok := err.(interface{ Temporary() bool }); !ok || temp.Temporary() {
		t.Fatalf("missing command must not be Temporary: %v", err)
	}
}

// 坏 JSON → 明确错误（health/poll/send 三条都试）。
func TestCommandDWSClientBadJSON(t *testing.T) {
	client := NewCommandDWSClient(DWSConfig{Command: writeDWSCommand(t, `#!/bin/sh
echo 'this is not json'
exit 0
`)})
	if _, err := client.AuthStatus(context.Background()); err == nil || !strings.Contains(err.Error(), "unreadable JSON") {
		t.Fatalf("AuthStatus err = %v", err)
	}
	if _, err := client.Poll(context.Background(), "", 50); err == nil || !strings.Contains(err.Error(), "unreadable JSON") {
		t.Fatalf("Poll err = %v", err)
	}
	if _, err := client.Send(context.Background(), "cid", "hi", "k"); err == nil || !strings.Contains(err.Error(), "unreadable JSON") {
		t.Fatalf("Send err = %v", err)
	}
}

// 超时：30s 契约在这里用 50ms 验证；错误可重试但不挂起。
func TestCommandDWSClientTimeout(t *testing.T) {
	client := NewCommandDWSClient(DWSConfig{
		Command: writeDWSCommand(t, "#!/bin/sh\nexec sleep 5\n"),
		Timeout: 50 * time.Millisecond,
	})
	start := time.Now()
	_, err := client.AuthStatus(context.Background())
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
	var de *DWSError
	if !errors.As(err, &de) || de.Permanent {
		t.Fatalf("timeout err = %v, want a retryable DWSError", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded in the chain", err)
	}
}

// ctx 取消：子进程被杀，调用立刻返回，脚本不会跑到"finished"。
func TestCommandDWSClientCancelKillsProcess(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	finished := filepath.Join(dir, "finished")
	client := NewCommandDWSClient(DWSConfig{
		Command: writeDWSCommand(t, `#!/bin/sh
echo started > `+started+`
sleep 5
echo finished > `+finished+`
`),
		Timeout: 10 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.AuthStatus(ctx)
		done <- err
	}()
	// 等脚本真的启动（最多 5s）。
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake dws never started")
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not unblock the call")
	}
	// 被 SIGKILL 的 shell 不会继续写到 finished。
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(finished); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled child kept running (finished exists: %v)", err)
	}
}

// health 子命令可选：返回 JSON 时解析；未知子命令时降级为 ErrDWSUndefined。
func TestCommandDWSClientHealth(t *testing.T) {
	ok := NewCommandDWSClient(DWSConfig{Command: writeDWSCommand(t, `#!/bin/sh
if [ "$1" = "health" ]; then echo '{"ok":true,"detail":"gateway fine"}'; exit 0; fi
exit 2
`)})
	h, err := ok.Health(context.Background())
	if err != nil || h.State != "ok" || h.Detail != "gateway fine" {
		t.Fatalf("Health = %+v, %v", h, err)
	}
	bad := NewCommandDWSClient(DWSConfig{Command: writeDWSCommand(t, `#!/bin/sh
if [ "$1" = "health" ]; then echo '{"ok":false,"detail":"gateway down"}'; exit 0; fi
exit 2
`)})
	h2, err := bad.Health(context.Background())
	if err != nil || h2.State != "error" || h2.Detail != "gateway down" {
		t.Fatalf("Health = %+v, %v", h2, err)
	}
	missing := NewCommandDWSClient(DWSConfig{Command: writeDWSCommand(t, `#!/bin/sh
echo "unknown command: health" >&2
exit 3
`)})
	if _, err := missing.Health(context.Background()); !errors.Is(err, ErrDWSUndefined) {
		t.Fatalf("err = %v, want ErrDWSUndefined", err)
	}
}

// marker 会去掉会截断注释的换行/制表符，其余原样。
func TestDWSMarkerFor(t *testing.T) {
	got := markerForDWS("k\n1\t2")
	if !strings.HasPrefix(got, "\n\n<!-- zeokeel:") {
		t.Fatalf("marker = %q, want a leading blank line + HTML comment", got)
	}
	if strings.ContainsAny(got, "\t") || strings.Count(got, "\n") != 2 {
		t.Fatalf("key newline/tab was not sanitized: %q", got)
	}
	if !strings.Contains(got, "<!-- zeokeel:k 1 2 -->") {
		t.Fatalf("marker = %q", got)
	}

	// 控制字符/行分隔符/HTML 注释破坏字符：marker 仍是合法 HTML 注释。
	hostile := []string{
		"cr\rlf\nline\u2028sep\u2029",
		"nul\x00bell\x07escape\x1b",
		"comment --> breakout",
		"double -- dash",
		"<script>alert(1)</script>",
	}
	for _, key := range hostile {
		marker := markerForDWS(key)
		if !strings.HasPrefix(marker, "\n\n<!-- zeokeel:") || !strings.HasSuffix(marker, " -->") {
			t.Fatalf("marker shape = %q", marker)
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(marker, "\n\n<!-- zeokeel:"), " -->")
		if strings.Contains(inner, "--") || strings.ContainsAny(inner, "<>") {
			t.Fatalf("marker inner is not a valid comment body: %q", inner)
		}
		for _, r := range inner {
			if r != ' ' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
				t.Fatalf("marker inner contains control/format rune %U: %q", r, inner)
			}
		}
		if strings.ContainsRune(inner, '\u2028') || strings.ContainsRune(inner, '\u2029') {
			t.Fatalf("marker inner contains line separator: %q", inner)
		}
	}
	// 危险 key 用 hex 编码：可反查原文。
	hexKey := "comment --> breakout"
	marker := markerForDWS(hexKey)
	inner := strings.TrimSuffix(strings.TrimPrefix(marker, "\n\n<!-- zeokeel:"), " -->")
	decoded, err := hex.DecodeString(inner)
	if err != nil || string(decoded) != hexKey {
		t.Fatalf("hex-encoded marker inner = %q (%v), want readable back to %q", inner, err, hexKey)
	}
}
