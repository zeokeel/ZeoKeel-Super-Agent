package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeSkillFile 在 <root>/<dir>/SKILL.md 写入内容。
func writeSkillFile(t *testing.T, root, dir, content string) {
	t.Helper()
	d := filepath.Join(root, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", d, err)
	}
	if err := os.WriteFile(filepath.Join(d, skillFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

// skillMD 拼一份规范的 SKILL.md。
func skillMD(name, desc, when, whenNot, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + name + "\n")
	b.WriteString("description: " + desc + "\n")
	b.WriteString("when-to-use: " + when + "\n")
	if whenNot != "" {
		b.WriteString("when-not-to-use: " + whenNot + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b.String()
}

// TestLoadValidSkills 覆盖：加载、字段解析、关键词拆分、排序、只读副本。
func TestLoadValidSkills(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, root, "go-test", skillMD(
		"go-test", "写 Go 测试与修失败用例", "test, 测试、单测；覆盖率", "只改文档时",
		"# Go 测试\n\n用 `go test ./...` 跑全量测试。"))
	writeSkillFile(t, root, "pdf", skillMD(
		"pdf", "PDF 抽取与生成", "pdf, 报表", "", "# PDF\n\n先 pdftotext。"))

	set, err := Load(root, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.Len() != 2 {
		t.Fatalf("期望 2 个技能，实际 %d: %v", set.Len(), set.Names())
	}
	// 名字排序是确定的。
	want := []string{"go-test", "pdf"}
	if got := set.Names(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Names = %v，期望 %v", got, want)
	}

	sk, ok := set.Get("go-test")
	if !ok {
		t.Fatal("Get(go-test) 失败")
	}
	if sk.Description != "写 Go 测试与修失败用例" {
		t.Errorf("description = %q", sk.Description)
	}
	if sk.WhenNotToUse != "只改文档时" {
		t.Errorf("when-not-to-use = %q", sk.WhenNotToUse)
	}
	// 中英文分隔符都要拆开，且去掉单字。
	if len(sk.Terms) != 4 {
		t.Errorf("Terms = %v，期望 4 个", sk.Terms)
	}
	if !strings.Contains(sk.Body, "go test ./...") {
		t.Errorf("正文没有解析出来: %q", sk.Body)
	}
	if !filepath.IsAbs(sk.Source) || !strings.HasSuffix(sk.Source, skillFileName) {
		t.Errorf("Source 应是绝对路径: %q", sk.Source)
	}
	if sk.Bytes == 0 {
		t.Error("Bytes 不应为 0")
	}

	// All() 必须返回副本：调用方改切片不能影响 Set。
	all := set.All()
	all[0] = nil
	if set.All()[0] == nil {
		t.Error("All() 没有返回副本")
	}
}

// TestLoadToleratesQuotesBOMAndCRLF 覆盖：真实文件里常见的三种写法。
// 理由：Windows 写的 SKILL.md 是 CRLF；描述里带冒号必须能加引号；编辑器可能留 BOM。
func TestLoadToleratesQuotesBOMAndCRLF(t *testing.T) {
	root := t.TempDir()
	content := "\ufeff---\r\n" +
		`name: "quoted"` + "\r\n" +
		`description: "Use when: the task needs quoted parsing"` + "\r\n" +
		"when-to-use: 'quoted, parsing'\r\n" +
		"---\r\n\r\nbody line\r\n"
	writeSkillFile(t, root, "quoted", content)

	set, err := Load(root, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := set.Get("quoted")
	if !ok {
		t.Fatalf("没有加载到技能: %v", set.Names())
	}
	if sk.Description != "Use when: the task needs quoted parsing" {
		t.Errorf("带引号的描述解析错误: %q", sk.Description)
	}
	if sk.Body != "body line" {
		t.Errorf("CRLF 正文解析错误: %q", sk.Body)
	}
}

// TestLoadRejectsBadFrontMatter 覆盖：结构错误必须带上文件路径报错。
func TestLoadRejectsBadFrontMatter(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantSub string
	}{
		{"no front matter", "# just markdown\n", "缺少 front matter"},
		{"unclosed", "---\nname: x\n", "没有结束标记"},
		{"bad line", "---\nname x\n---\nbody\n", "不是 key: value"},
		{"dup key", "---\nname: a\nname: b\ndescription: d\nwhen-to-use: t\n---\nbody\n", "重复"},
		{"unclosed quote", "---\nname: a\ndescription: \"oops\nwhen-to-use: t\n---\nbody\n", "没有闭合"},
		{"missing name", "---\ndescription: d\nwhen-to-use: t\n---\nbody\n", "缺少必填字段 name"},
		{"missing description", "---\nname: a\nwhen-to-use: t\n---\nbody\n", "缺少必填字段 description"},
		{"missing when-to-use", "---\nname: a\ndescription: d\n---\nbody\n", "缺少必填字段 when-to-use"},
		{"empty body", "---\nname: a\ndescription: d\nwhen-to-use: tests\n---\n\n", "正文为空"},
		{"short terms only", "---\nname: a\ndescription: d\nwhen-to-use: ，,;、\n---\nbody\n", "没有可用的关键词"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSkillFile(t, root, "a", tc.content)
			_, err := Load(root, Options{})
			if err == nil {
				t.Fatalf("期望报错，实际成功")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("错误 %q 缺少 %q", err, tc.wantSub)
			}
			if !strings.Contains(err.Error(), "SKILL.md") {
				t.Errorf("错误里没有文件路径: %v", err)
			}
		})
	}
}

// TestLoadValidatesName 覆盖：命名规则与"name 必须等于目录名"。
func TestLoadValidatesName(t *testing.T) {
	cases := []struct {
		name    string
		dir     string
		field   string
		wantSub string
	}{
		{"kebab", "go-test", "go-test", ""},
		{"underscore", "go_test", "go_test", "不是 kebab-case"},
		{"uppercase", "GoTest", "GoTest", "不是 kebab-case"},
		{"path separator", "sub", "../evil", "不是 kebab-case"},
		{"mismatch", "dir-name", "other-name", "与目录名"},
		{"too long", strings.Repeat("a", 70), strings.Repeat("a", 70), "超过"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSkillFile(t, root, tc.dir, skillMD(tc.field, "d", "when", "", "body"))
			_, err := Load(root, Options{})
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("不该报错: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err = %v，期望包含 %q", err, tc.wantSub)
			}
		})
	}
}

// TestLoadRejectsOversizeSkill 覆盖：超长 SKILL.md 报错。
func TestLoadRejectsOversizeSkill(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, root, "big", skillMD("big", "d", "when", "", strings.Repeat("x", 200)))
	_, err := Load(root, Options{MaxSkillBytes: 100})
	if err == nil || !strings.Contains(err.Error(), "太大") {
		t.Fatalf("err = %v，期望超长报错", err)
	}
}

// TestLoadRejectsDuplicateNamesAcrossRoots 覆盖：重名必须报错，不能后者覆盖前者。
func TestLoadRejectsDuplicateNamesAcrossRoots(t *testing.T) {
	r1, r2 := t.TempDir(), t.TempDir()
	writeSkillFile(t, r1, "dup", skillMD("dup", "第一份", "alpha", "", "body1"))
	writeSkillFile(t, r2, "dup", skillMD("dup", "第二份", "beta", "", "body2"))
	_, err := LoadDirs([]string{r1, r2}, Options{})
	if err == nil || !strings.Contains(err.Error(), "重名") {
		t.Fatalf("err = %v，期望重名报错", err)
	}
}

// TestLoadSkipsNonSkillEntries 覆盖：非技能目录/文件/隐藏目录静默跳过。
func TestLoadSkipsNonSkillEntries(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, root, "real", skillMD("real", "d", "when", "", "body"))
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil { // 没有 SKILL.md
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Load(root, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.Len() != 1 || set.Names()[0] != "real" {
		t.Errorf("只应加载 real，实际 %v", set.Names())
	}
}

// TestLoadRejectsTooManySkills 覆盖：数量上限。
func TestLoadRejectsTooManySkills(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"a-one", "b-two", "c-three"} {
		writeSkillFile(t, root, n, skillMD(n, "d", "when", "", "body"))
	}
	_, err := Load(root, Options{MaxSkills: 1})
	if err == nil || !strings.Contains(err.Error(), "超过上限") {
		t.Fatalf("err = %v，期望数量上限报错", err)
	}
}

// TestLoadMissingRoot 覆盖：根目录不存在要报错（而不是当成"没有技能"）。
func TestLoadMissingRoot(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope"), Options{})
	if err == nil || !strings.Contains(err.Error(), "根目录") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Load("", Options{}); err == nil {
		t.Fatal("空根目录应报错")
	}
}

// TestSimlinkEscapeRejected 覆盖：符号链接逃出根目录必须拒绝。
// 这是提示注入的入口：/etc 下的文件不该能变成技能正文。
func TestSymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接在 Windows 上需要额外权限")
	}
	outside := t.TempDir()
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, skillFileName),
		[]byte(skillMD("evil", "d", "when", "", "secrets")), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "evil")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	_, err := Load(root, Options{})
	if err == nil || !strings.Contains(err.Error(), "逃出了根目录") {
		t.Fatalf("err = %v，期望拒绝越界符号链接", err)
	}
}

// TestSymlinkInsideAllowed 覆盖：指向根目录内部的符号链接应当允许。
func TestSymlinkInsideAllowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接在 Windows 上需要额外权限")
	}
	root := t.TempDir()
	writeSkillFile(t, root, "real", skillMD("real", "d", "when", "", "body"))
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	// alias 解析后指向真实目录 real，是同一个技能的两个入口：
	// 应当被接受、且按真实路径去重，只算一个技能。
	set, err := Load(root, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.Len() != 1 {
		t.Errorf("内部符号链接应去重为 1 个技能，实际: %v", set.Names())
	}
}

// TestSymlinkedSkillFileEscapes 覆盖：目录在根内、但 SKILL.md 是指向根外的
// 符号链接时，必须拒绝——否则任意文件都能变成系统提示的一部分。
func TestSymlinkedSkillFileEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接在 Windows 上需要额外权限")
	}
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("TOP-SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "evil")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, skillFileName)); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	_, err := Load(root, Options{})
	if err == nil || !strings.Contains(err.Error(), "指向根目录外") {
		t.Fatalf("err = %v，期望拒绝根目录外的 SKILL.md 符号链接", err)
	}
}

// TestWhenToUseAliases 覆盖：when_to_use 这种下划线写法也接受。
func TestWhenToUseAliases(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, root, "alias", "---\nname: alias\ndescription: d\nwhen_to_use: foo, bar\n---\nbody\n")
	set, err := Load(root, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, _ := set.Get("alias")
	if len(sk.Terms) != 2 {
		t.Errorf("Terms = %v", sk.Terms)
	}
}

// TestSymlinkRelativeEscapeRejected 覆盖：指向根外的相对符号链接同样必须拒绝
// （os.Root 在系统调用层面拒绝，不再依赖事后的 EvalSymlinks 比较）。
func TestSymlinkRelativeEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接在 Windows 上需要额外权限")
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, skillFileName),
		[]byte(skillMD("evil", "d", "when", "", "secrets")), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	// ../outside 从根目录内出发指向根外。
	if err := os.Symlink(filepath.Join("..", filepath.Base(outside)), filepath.Join(root, "evil")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	_, err := Load(root, Options{})
	if err == nil || !strings.Contains(err.Error(), "逃出了根目录") {
		t.Fatalf("err = %v，期望拒绝相对路径越界符号链接", err)
	}
}

// TestSymlinkToSkillFileInsideStillLoads 覆盖：SKILL.md 是指向根内文件的符号链接
// 仍然可用（绝对链接也要按根内相对路径映射，而不是被 os.Root 直接拒掉）。
func TestSymlinkToSkillFileInsideStillLoads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接在 Windows 上需要额外权限")
	}
	root := t.TempDir()
	real := filepath.Join(root, "body.md")
	if err := os.WriteFile(real, []byte(skillMD("real", "d", "when", "", "body")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "real")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, skillFileName)); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	set, err := Load(root, Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.Len() != 1 || set.Names()[0] != "real" {
		t.Fatalf("names = %v，期望加载根内符号链接的技能", set.Names())
	}
}
