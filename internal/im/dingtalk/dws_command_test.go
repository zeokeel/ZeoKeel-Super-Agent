package dingtalk

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
)

// These tests run a temporary POSIX shell script as the `dws` CLI: they verify
// the exact command templates, NDJSON streaming, marker handling and process
// lifecycle without a real DWS install or network.

func requirePOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("dws CLI contract tests use POSIX shell scripts")
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

func readArgsNUL(t *testing.T, path string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	parts := bytes.Split(raw, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// auth status: --format json + --profile；登录态与登录命令。
func TestCommandDWSAuthStatus(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "auth.args")
	cmd := writeDWSCommand(t, `#!/bin/sh
printf '%s\0' "$@" > `+argsFile+`
echo '{"loggedIn":false,"account":"","profile":"work","authUrl":"https://login.example/x"}'
`)
	ch, err := NewDWS(DWSConfig{Command: cmd, Profile: "work", LoginHeadless: true})
	if err != nil {
		t.Fatalf("NewDWS: %v", err)
	}
	defer ch.Close()
	st, err := ch.AuthStatus(context.Background())
	if err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	if st.LoggedIn || st.AuthURL != "https://login.example/x" || st.Profile != "work" {
		t.Fatalf("auth = %+v", st)
	}
	if !strings.Contains(st.LoginCommand, "auth login --device") {
		t.Fatalf("login command = %q", st.LoginCommand)
	}
	args := readArgsNUL(t, argsFile)
	joined := string(bytes.Join(args, []byte(" ")))
	if !strings.Contains(joined, "auth status --format json") || !strings.Contains(joined, "--profile work") {
		t.Fatalf("auth args = %q", args)
	}
	if state, detail := ch.Status(); state != "auth" || !strings.Contains(detail, "dws 未登录") {
		t.Fatalf("status = %q/%q", state, detail)
	}
}

// 长驻 NDJSON consumer：ready marker、事件解析、Close 杀子进程。
func TestCommandDWSListenNDJSONAndClose(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	finished := filepath.Join(dir, "finished")
	line := `{"event_id":"e1","event_type":"user_im_message_receive_group_all",` +
		`"conversation_id":"cid_g","conversation_type":"group","sender":"张三",` +
		`"sender_open_dingtalk_id":"u1","content":{"text":"@bot 帮我看下"},` +
		`"create_time":1700000000000}`
	cmd := writeDWSCommand(t, `#!/bin/sh
echo started > `+started+`
echo '[event] ready' >&2
printf '%s\n' '`+line+`'
sleep 5
echo finished > `+finished+`
`)
	ch, err := NewDWS(DWSConfig{Command: cmd, ListenKind: ListenAtMe, Backoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewDWS: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan im.RawEvent, 4)
	done := make(chan error, 1)
	go func() { done <- ch.Subscribe(ctx, out) }()

	var ev im.RawEvent
	select {
	case ev = <-out:
	case <-time.After(5 * time.Second):
		t.Fatal("no RawEvent from the NDJSON consumer")
	}
	if ev.ID != "e1" || ev.Conv.ID != "cid_g" || ev.Conv.Kind != domain.ConvGroup ||
		ev.Actor.ID != "u1" || ev.Text != "@bot 帮我看下" || !ev.AddressedToBot {
		t.Fatalf("raw event = %+v", ev)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatalf("consumer process never started: %v", err)
	}
	if err := ch.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Subscribe did not stop after Close")
	}
	// 子进程被杀，不会跑到 finished。
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(finished); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("consumer kept running after Close (finished exists: %v)", err)
	}
	if state, _ := ch.Status(); state != "stopped" {
		t.Fatalf("state = %q, want stopped", state)
	}
}

// 发送模板：+messages-send --as user --group/--open-dingtalk-id + marker；
// 文本不经过 shell（命令替换不执行）。
func TestCommandDWSSendTemplateAndNoShellInterpolation(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "send.args")
	sentinel := filepath.Join(dir, "pwned")
	cmd := writeDWSCommand(t, `#!/bin/sh
printf '%s\0' "$@" > `+argsFile+`
if [ -n "$DWS_SEND_FAIL" ]; then echo "HTTP 404 Not Found" >&2; exit 1; fi
echo '{"messageId":"m9"}'
`)
	ch, err := NewDWS(DWSConfig{Command: cmd, Profile: "work"})
	if err != nil {
		t.Fatalf("NewDWS: %v", err)
	}
	defer ch.Close()
	text := "hello; $(touch " + sentinel + ") `touch " + sentinel + "`"
	receipt, err := ch.Send(context.Background(), im.Outbound{
		Key: "k-1", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid_g"}, Text: text,
	})
	if err != nil || receipt.MessageID != "m9" {
		t.Fatalf("Send = %+v, %v", receipt, err)
	}
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("send text was shell-interpolated (sentinel exists: %v)", err)
	}
	args := readArgsNUL(t, argsFile)
	joined := string(bytes.Join(args, []byte(" ")))
	if !strings.Contains(joined, "chat +messages-send --as user --group cid_g --text "+text) {
		t.Fatalf("send args = %q", args)
	}
	if !strings.Contains(joined, "<!-- zeokeel:k-1 -->") || !strings.Contains(joined, "--format json") {
		t.Fatalf("marker/format missing: %q", joined)
	}
	if strings.Contains(joined, "--idempotency-key") {
		t.Fatalf("DWS has no idempotency flag: %q", joined)
	}

	// 单聊：dm: → --open-dingtalk-id
	if _, err := ch.Send(context.Background(), im.Outbound{
		Key: "k-2", Conv: domain.Conversation{Kind: domain.ConvDirect, ID: "dm:u9"}, Text: "hi",
	}); err != nil {
		t.Fatal(err)
	}
	args = readArgsNUL(t, argsFile)
	joined = string(bytes.Join(args, []byte(" ")))
	if !strings.Contains(joined, "--open-dingtalk-id u9") || strings.Contains(joined, "--group") {
		t.Fatalf("direct send args = %q", joined)
	}
}

// 4xx 永久 / 5xx 可重试通过真实退出码 + stderr 分类。
func TestCommandDWSSendStatusClassification(t *testing.T) {
	cmd := writeDWSCommand(t, `#!/bin/sh
if [ "$DWS_STATUS" = "404" ]; then echo "HTTP 404 Not Found" >&2; exit 1; fi
if [ "$DWS_STATUS" = "503" ]; then echo "HTTP 503 Service Unavailable" >&2; exit 1; fi
echo '{"messageId":"m1"}'
`)
	t.Setenv("DWS_STATUS", "404")
	ch, _ := NewDWS(DWSConfig{Command: cmd})
	if _, err := ch.Send(context.Background(), im.Outbound{Key: "k", Conv: domain.Conversation{ID: "cid"}, Text: "x"}); !IsPermanent(err) {
		t.Fatalf("404 err = %v, want permanent", err)
	}
	ch.Close()
	t.Setenv("DWS_STATUS", "503")
	ch2, _ := NewDWS(DWSConfig{Command: cmd})
	if _, err := ch2.Send(context.Background(), im.Outbound{Key: "k", Conv: domain.Conversation{ID: "cid"}, Text: "x"}); err == nil || IsPermanent(err) || !IsRetryable(err) {
		t.Fatalf("503 err = %v, want retryable", err)
	}
	ch2.Close()
}

// 命令缺失 → 永久错误 + 可执行 detail；status 变 error。
func TestCommandDWSMissingExecutable(t *testing.T) {
	ch, err := NewDWS(DWSConfig{Command: filepath.Join(t.TempDir(), "no-such-dws")})
	if err != nil {
		t.Fatalf("NewDWS: %v", err)
	}
	defer ch.Close()
	_, err = ch.AuthStatus(context.Background())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("err = %v, want permanent command error", err)
	}
	if !strings.Contains(err.Error(), "未安装") && !strings.Contains(strings.ToLower(err.Error()), "not executable") {
		t.Fatalf("detail = %q, want actionable install message", err.Error())
	}
	if state, _ := ch.Status(); state != "auth" && state != "error" {
		t.Fatalf("state = %q", state)
	}
}

// 30s 契约用 50ms 验证：超时返回 DeadlineExceeded，不挂起。
func TestCommandDWSTimeout(t *testing.T) {
	requirePOSIX(t)
	cmd := writeDWSCommand(t, "#!/bin/sh\nexec sleep 5\n")
	ch, err := NewDWS(DWSConfig{Command: cmd, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	start := time.Now()
	_, err = ch.AuthStatus(context.Background())
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout err = %v after %v", err, time.Since(start))
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

// ctx 取消立即杀子进程（脚本不会跑到 finished）。
func TestCommandDWSCancelKillsChild(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	finished := filepath.Join(dir, "finished")
	cmd := writeDWSCommand(t, `#!/bin/sh
echo started > `+started+`
sleep 5
echo finished > `+finished+`
`)
	ch, err := NewDWS(DWSConfig{Command: cmd, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ch.AuthStatus(ctx)
		done <- err
	}()
	waitForCond(t, func() bool { _, err := os.Stat(started); return err == nil }, "fake dws start")
	cancel()
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not unblock the command")
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(finished); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled child kept running (finished exists: %v)", err)
	}
}

// marker 形态：默认 HTML 注释；零宽模式不可见；危险 key 不出现在注释体。
func TestDWSMarkerModes(t *testing.T) {
	html := MarkerFor("k-42", MarkerHTMLComment)
	if !strings.Contains(html, "\n\n<!-- zeokeel:k-42 -->") || !MarkerPossiblyVisible(MarkerHTMLComment) {
		t.Fatalf("html marker = %q", html)
	}
	zero := MarkerFor("k-42", MarkerZeroWidth)
	if !HasMarker(zero, MarkerZeroWidth) || MarkerPossiblyVisible(MarkerZeroWidth) {
		t.Fatalf("zero-width marker = %q", zero)
	}
	if StripMarker(zero, MarkerZeroWidth) != "" {
		t.Fatalf("strip zero = %q", StripMarker(zero, MarkerZeroWidth))
	}
	hostile := MarkerFor("a--b<script>", MarkerHTMLComment)
	inner := strings.TrimSuffix(strings.TrimPrefix(hostile, "\n\n<!-- zeokeel:"), " -->")
	if strings.Contains(inner, "--") || strings.ContainsAny(inner, "<>") {
		t.Fatalf("invalid comment body = %q", inner)
	}
	if !HasMarker("正文"+html, MarkerHTMLComment) || StripMarker("正文"+html, MarkerHTMLComment) != "正文" {
		t.Fatal("HTML marker detection/strip failed")
	}
}
