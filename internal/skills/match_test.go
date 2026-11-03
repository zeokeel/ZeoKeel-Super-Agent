package skills

import (
	"sort"
	"strings"
	"testing"
)

// newTestSet 直接构造 Set，避免每个匹配用例都写临时文件。
// Set 创建后只读，所以测试里直接填字段是安全的。
func newTestSet(skills ...*Skill) *Set {
	set := &Set{opts: Options{}.withDefaults(), byName: map[string]*Skill{}}
	for _, sk := range skills {
		set.skills = append(set.skills, sk)
		set.byName[sk.Name] = sk
	}
	sort.Slice(set.skills, func(i, j int) bool { return set.skills[i].Name < set.skills[j].Name })
	return set
}

func testSkill(name, desc, when string) *Skill {
	return &Skill{
		Name:        name,
		Description: desc,
		WhenToUse:   when,
		Terms:       splitTerms(when),
		Body:        "body of " + name,
		Source:      "/skills/" + name + "/SKILL.md",
	}
}

func namesOf(ms []Match) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Skill.Name)
	}
	return out
}

// TestMatchDeterministic 覆盖：同一任务反复匹配必须得到完全相同的顺序与理由。
// 这是"不调模型"要换来的核心保证。
func TestMatchDeterministic(t *testing.T) {
	set := newTestSet(
		testSkill("go-test", "写 Go 测试并修失败用例", "test, 测试, 单测"),
		testSkill("deploy", "部署到预发或生产", "部署, release"),
		testSkill("pdf", "PDF 报表生成", "pdf, 报表"),
	)
	task := "请给 deploy 模块补测试，然后部署到预发"
	first := set.Match(task, 3)
	if len(first) == 0 {
		t.Fatal("应有候选")
	}
	for i := 0; i < 20; i++ {
		got := set.Match(task, 3)
		if strings.Join(namesOf(got), ",") != strings.Join(namesOf(first), ",") {
			t.Fatalf("第 %d 次匹配顺序不同: %v vs %v", i, namesOf(got), namesOf(first))
		}
		for j := range got {
			if got[j].Score != first[j].Score {
				t.Fatalf("第 %d 次匹配分数不同", i)
			}
			if strings.Join(got[j].Reasons, "|") != strings.Join(first[j].Reasons, "|") {
				t.Fatalf("第 %d 次匹配理由不同", i)
			}
		}
	}
}

// TestMatchPrefersWhenToUse 覆盖：显式触发词的权重高于描述命中。
func TestMatchPrefersWhenToUse(t *testing.T) {
	explicit := testSkill("explicit", "无关描述", "deploy")
	descOnly := testSkill("desc-only", "deploy deployment release 工具", "unrelated")
	set := newTestSet(explicit, descOnly)

	got := set.Match("please deploy it", 2)
	if len(got) != 2 {
		t.Fatalf("期望 2 个候选，实际 %v", namesOf(got))
	}
	if got[0].Skill.Name != "explicit" {
		t.Errorf("显式 when-to-use 应排第一，实际 %v (分数 %d/%d)",
			namesOf(got), got[0].Score, got[1].Score)
	}
}

// TestMatchEnglishWordBoundary 覆盖：英文按词边界匹配，"test" 不能命中 "contest"。
func TestMatchEnglishWordBoundary(t *testing.T) {
	set := newTestSet(testSkill("go-test", "写测试", "test"))

	if got := set.Match("we won the contest", 3); len(got) != 0 {
		t.Errorf("contest 不该命中 test: %v", namesOf(got))
	}
	if got := set.Match("please run the test suite", 3); len(got) == 0 {
		t.Error("test 应命中 test suite")
	}
	// 大小写不敏感。
	if got := set.Match("Run TEST now", 3); len(got) == 0 {
		t.Error("匹配应大小写不敏感")
	}
}

// TestMatchChineseSubstring 覆盖：中文按子串匹配（没有分词器）。
func TestMatchChineseSubstring(t *testing.T) {
	set := newTestSet(testSkill("deploy", "部署", "部署, 预发"))
	got := set.Match("帮忙把服务部署到预发环境", 3)
	if len(got) == 0 {
		t.Fatal("中文任务应命中")
	}
	if got[0].Score < weightWhenToUse {
		t.Errorf("中文命中分数过低: %d", got[0].Score)
	}
}

// TestMatchLimit 覆盖：limit 与默认候选数。
func TestMatchLimit(t *testing.T) {
	set := newTestSet(
		testSkill("aaa", "d", "deploy"),
		testSkill("bbb", "d", "deploy"),
		testSkill("ccc", "d", "deploy"),
		testSkill("ddd", "d", "deploy"),
	)
	if got := set.Match("deploy now", 2); len(got) != 2 {
		t.Errorf("limit=2 实际 %d", len(got))
	}
	// limit<=0 用默认值 MaxCandidates(3)。
	if got := set.Match("deploy now", 0); len(got) != 3 {
		t.Errorf("默认 limit 应为 3，实际 %d", len(got))
	}
}

// TestMatchTieBreaksByName 覆盖：同分时按名字升序，保证稳定。
func TestMatchTieBreaksByName(t *testing.T) {
	set := newTestSet(
		testSkill("zeta", "d", "deploy"),
		testSkill("alpha", "d", "deploy"),
	)
	got := set.Match("deploy", 2)
	if len(got) != 2 {
		t.Fatalf("期望 2 个，实际 %v", namesOf(got))
	}
	if got[0].Skill.Name != "alpha" || got[1].Skill.Name != "zeta" {
		t.Errorf("同分应按名字升序，实际 %v", namesOf(got))
	}
}

// TestMatchNoHitAndEmptyTask 覆盖：无命中返回空，空任务不做匹配。
func TestMatchNoHitAndEmptyTask(t *testing.T) {
	set := newTestSet(testSkill("pdf", "PDF", "pdf"))
	if got := set.Match("写一个 Go 单元测试", 3); len(got) != 0 {
		t.Errorf("无命中应返回空: %v", namesOf(got))
	}
	if got := set.Match("   ", 3); len(got) != 0 {
		t.Errorf("空任务应返回空: %v", namesOf(got))
	}
	if got := newTestSet().Match("deploy", 3); len(got) != 0 {
		t.Errorf("空 Set 应返回空: %v", namesOf(got))
	}
}

// TestMatchReasonsAreExplainable 覆盖：命中理由要能指出命中了哪个词。
func TestMatchReasonsAreExplainable(t *testing.T) {
	set := newTestSet(testSkill("deploy", "部署工具", "deploy, 预发"))
	got := set.Match("deploy 到预发", 1)
	if len(got) != 1 {
		t.Fatalf("应有 1 个候选: %v", namesOf(got))
	}
	joined := strings.Join(got[0].Reasons, "；")
	if !strings.Contains(joined, "when-to-use 命中") || !strings.Contains(joined, "deploy") {
		t.Errorf("理由不可解释: %q", joined)
	}
}

// TestDescribeTerms 覆盖：描述抽词（英文单词 + 中文二元组 + 停用词过滤）。
func TestDescribeTerms(t *testing.T) {
	terms := describeTerms("PDF generation for the 中文测试 report")
	joined := strings.Join(terms, ",")
	for _, want := range []string{"pdf", "generation", "report", "中文", "文测", "测试"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少词 %q，实际 %v", want, terms)
		}
	}
	// 停用词不该成为匹配信号：几乎所有描述里都有 "the"/"for"。
	for _, bad := range []string{"the", "for"} {
		for _, term := range terms {
			if term == bad {
				t.Errorf("停用词 %q 不该进入匹配词表: %v", bad, terms)
			}
		}
	}
	// 太短的英文词被丢弃。
	for _, term := range terms {
		if isASCII(term) && len(term) < 3 {
			t.Errorf("英文词 %q 过短", term)
		}
	}
}

// TestContainsTermBoundaries 覆盖词边界判断的边角情况。
func TestContainsTermBoundaries(t *testing.T) {
	cases := []struct {
		hay, term string
		want      bool
	}{
		{"deploy now", "deploy", true},
		{"deploying", "deploy", false}, // 单词内不匹配
		{"re-deploy", "deploy", true},  // 连字符是边界
		{"部署到预发", "部署", true},
		{"", "deploy", false},
		{"deploy", "", false},
		{"multi word term", "word term", true},
	}
	for _, tc := range cases {
		if got := containsTerm(strings.ToLower(tc.hay), tc.term); got != tc.want {
			t.Errorf("containsTerm(%q,%q) = %v，期望 %v", tc.hay, tc.term, got, tc.want)
		}
	}
}
