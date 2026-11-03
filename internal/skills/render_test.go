package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// loadTwoSkills 在临时目录里放两个技能，返回加载好的 Set。
func loadTwoSkills(t *testing.T) *Set {
	t.Helper()
	root := t.TempDir()
	writeSkillFile(t, root, "deploy", skillMD(
		"deploy", "部署到预发或生产", "deploy, 部署", "只改文档时",
		"# 部署\n\n1. 跑 `make deploy`\n2. 看面板确认"))
	writeSkillFile(t, root, "pdf", skillMD(
		"pdf", "PDF 报表生成", "pdf, 报表", "",
		"# PDF\n\n用 pdftotext 抽取。"))
	set, err := Load(root, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return set
}

// TestRenderPromptEndToEnd 覆盖：任务相关技能被注入，无关技能不出现。
func TestRenderPromptEndToEnd(t *testing.T) {
	set := loadTwoSkills(t)
	out := set.RenderPrompt("把服务部署到预发", 0)
	if out == "" {
		t.Fatal("应有渲染结果")
	}
	if !strings.Contains(out, "deploy") {
		t.Errorf("应注入 deploy 技能: %s", out)
	}
	if strings.Contains(out, "### 1. pdf") || strings.Contains(out, "### 2. pdf") {
		t.Errorf("无关的 pdf 技能不该注入: %s", out)
	}
}

// TestRenderIncludesBoundaries 覆盖：渲染必须带"何时用/何时别用/来源/正文"。
// 边界是这套机制的核心：没有明确的"何时不要用"，模型会为了用上技能而扭曲任务。
func TestRenderIncludesBoundaries(t *testing.T) {
	set := loadTwoSkills(t)
	matches := set.Match("deploy", 1)
	if len(matches) != 1 {
		t.Fatalf("期望 1 个匹配，实际 %v", namesOf(matches))
	}
	out := set.Render(matches)

	for _, want := range []string{
		"## 可用技能",
		"### 1. deploy",
		"- 描述：部署到预发或生产",
		"- 何时使用：deploy, 部署",
		"- 何时不要使用：只改文档时",
		"- 来源：",
		"SKILL.md",
		"- 命中理由：",
		"make deploy",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("渲染结果缺少 %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "不要因为技能存在就强行套用") {
		t.Errorf("缺少「不要强行套用」的约束: %s", out)
	}
}

// TestRenderGenericBoundaryWhenMissing 覆盖：没写 when-not-to-use 时给通用边界，
// 不能让"何时不要使用"这一项直接消失。
func TestRenderGenericBoundaryWhenMissing(t *testing.T) {
	set := loadTwoSkills(t)
	matches := set.Match("pdf 报表", 1)
	if len(matches) != 1 {
		t.Fatalf("期望 1 个匹配: %v", namesOf(matches))
	}
	out := set.Render(matches)
	if !strings.Contains(out, "何时不要使用：未显式声明") {
		t.Errorf("缺少通用边界: %s", out)
	}
}

// TestRenderEmpty 覆盖：没有匹配就返回空串，调用方可以据此跳过拼接。
func TestRenderEmpty(t *testing.T) {
	set := loadTwoSkills(t)
	if out := set.Render(nil); out != "" {
		t.Errorf("空匹配应返回空串，实际 %q", out)
	}
	if out := set.RenderPrompt("完全无关的任务，比如写诗", 0); out != "" {
		t.Errorf("无匹配任务应返回空串，实际 %q", out)
	}
}

// TestRenderDeterministic 覆盖：同样输入两次渲染字节完全一致（提示词可 diff）。
func TestRenderDeterministic(t *testing.T) {
	set := loadTwoSkills(t)
	a := set.RenderPrompt("deploy", 0)
	b := set.RenderPrompt("deploy", 0)
	if a != b {
		t.Fatalf("渲染不确定:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
	if a == "" {
		t.Fatal("应有内容")
	}
}

// TestRenderRespectsBudget 覆盖：总预算有限时截断而不是无限膨胀。
func TestRenderRespectsBudget(t *testing.T) {
	root := t.TempDir()
	bigBody := strings.Repeat("这是一段很长的技能正文。\n", 500)
	for _, n := range []string{"aaa", "bbb", "ccc"} {
		writeSkillFile(t, root, n, skillMD(n, "部署 deploy", "deploy", "", bigBody))
	}
	set, err := Load(root, Options{MaxPromptBytes: 1200})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := set.RenderPrompt("deploy", 3)
	if !utf8.ValidString(out) {
		t.Fatal("截断后出现非法 UTF-8")
	}
	// 允许比预算多一点（截断提示本身要占字符），但绝不能接近原始大小。
	if len(out) > 1200+400 {
		t.Errorf("渲染结果 %d 字节，超出预算太多", len(out))
	}
	if !strings.Contains(out, "截断") && !strings.Contains(out, "未展开") {
		t.Errorf("预算紧张时应有截断/未展开提示: %s", out)
	}
}

// TestRenderBudgetTinyHeaderStillExplains 覆盖：预算小到连一个块都放不下时，
// 也必须告诉模型"还有技能没展开"，而不是静默丢信息。
func TestRenderBudgetTinyHeaderStillExplains(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, root, "aaa", skillMD("aaa", "部署 deploy", "deploy", "", strings.Repeat("正文", 100)))
	set, err := Load(root, Options{MaxPromptBytes: 50})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := set.RenderPrompt("deploy", 1)
	if !strings.Contains(out, "未展开") {
		t.Errorf("预算不足时应有未展开提示: %q", out)
	}
}

// TestRenderPromptFromDiskWithChineseTask 覆盖：真实目录加载 + 中文任务端到端。
func TestRenderPromptFromDiskWithChineseTask(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, root, "go-test", skillMD("go-test", "写 Go 测试", "测试, 单测", "只改文档",
		"跑 go test ./..."))
	writeSkillFile(t, root, "deploy", skillMD("deploy", "部署服务", "部署, 发布", "",
		"跑 make deploy"))
	set, err := Load(root, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// 技能正文要能被注入；路径不能是相对路径（面板要能显示它的位置）。
	out := set.RenderPrompt("帮我写单测", 0)
	if !strings.Contains(out, "go-test") || strings.Contains(out, "### 1. deploy") {
		t.Errorf("中文任务匹配结果不对:\n%s", out)
	}
	matches := set.Match("帮我写单测", 1)
	if !filepath.IsAbs(matches[0].Skill.Source) {
		t.Errorf("Source 应是绝对路径: %q", matches[0].Skill.Source)
	}
	if _, err := os.Stat(matches[0].Skill.Source); err != nil {
		t.Errorf("Source 文件应存在: %v", err)
	}
}
