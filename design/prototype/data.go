package main

// Sample data for the prototype. It mirrors the shapes the real view models
// (internal/view) will expose: everything here is read-only, already derived,
// and safe to render on the main thread.
type needItem struct {
	source, sourceText, sourceBG string
	id, title, reason            string
	stage, stageFG, stageBG      string
	age                          string
}

var needs = []needItem{
	{"GH", "GH", "#24292F", "#42", "修改登录超时配置", "等待你确认配置名（ask_user_question）", "等待回答", "#B54708", "#FEF3D6", "12m"},
	{"飞", "飞", "#3370FF", "#44", "CI 失败：auth 包测试超时", "Blocked：等待授权重跑流水线", "需要授权", "#B42318", "#FEECEB", "34m"},
	{"GH", "GH", "#24292F", "#45", "为 session 配置补迁移脚本", "PR 已就绪，等待你 Review", "等待评审", "#6941C6", "#F1EDFF", "1h"},
}

type artifact struct {
	icon      string
	name      string
	meta      string
	tone      string
	toneColor string
}

var recentArtifacts = []artifact{
	{"branch", "PR #128 — 支持 session_timeout 配置", "+128 −36 · 9 files", "PR", "#2F5BFF"},
	{"file", "report.md — 变更说明", "4.2 KB · 刚刚", "MD", "#6941C6"},
	{"image", "login-timeout.png — 运行截图", "1440×900 · 3 分钟前", "PNG", "#0EA5C6"},
}

type planStep struct {
	state int // 0 todo, 1 doing, 2 done
	text  string
}

var plan = []planStep{
	{2, "定位认证模块里的超时硬编码"},
	{1, "抽出 SessionTimeout 配置项与默认值"},
	{0, "补测试与文档，更新 Ticket"},
}

type toolCall struct {
	kind   string // bash | edit | test
	title  string
	detail string
	result string
	time   string
}

var transcriptTools = []toolCall{
	{"bash", "bash", `rg -n "15 * time.Minute" internal/auth`, "03 处 · 0.4s", "exit 0"},
	{"edit", "edit", "internal/auth/session.go", "+12 −3 · 0.2s", "已写入"},
	{"test", "bash", "go test ./internal/auth/...", "ok 1.82s · 2.1s", "exit 0"},
}

type threadEvent struct {
	kind, title, reason, at string
}

var threadEvents = []threadEvent{
	{"trigger", "Issue #42 新建：登录超时应该可配置", "acme/webapp · 由 @lin 创建", "12:04"},
	{"decision", "评估：与已有 Ticket #38 不同，新建 Ticket", "命中规则「Issue 含可配置/默认值 → 建 Ticket」", "12:04"},
	{"trigger", "PR #128 CI 失败：TestSessionTimeout", "acme/webapp · workflow ci", "12:31"},
	{"decision", "归入 Thread #42（follows 现有 Ticket）", "同一分支 · 订阅命中", "12:31"},
	{"context", "飞书 研发群：讨论了超时默认值", "仅作为上下文，不改变权限", "12:40"},
}
