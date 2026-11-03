package skills

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 匹配权重。数字本身不重要，重要的是次序：when-to-use 是作者显式声明的触发词，
// 比名字和描述更可信，所以权重最高。
const (
	weightWhenToUse = 3
	weightName      = 2
	weightDesc      = 1
	// maxDescHits 给描述命中封顶：描述通常很长，不封顶会让"描述写得多"的技能
	// 仅靠字数赢过真正相关的技能。
	maxDescHits = 3
	// maxDescTerms 限制从描述里抽出的词数，避免长描述产生几百次比较。
	maxDescTerms = 24
)

// Match 是一个候选技能及其可解释的打分。
type Match struct {
	Skill *Skill
	Score int
	// Reasons 记录命中了哪些词，供面板展示"为什么注入这个技能"。
	// 没有它，技能注入就是个黑盒，长任务复盘时无法解释。
	Reasons []string
}

// Match 按任务文本挑选候选技能。
//
// 纯函数、无 I/O、无随机：同样的 task 永远返回同样的顺序。排序键是
// (分数降序, 名字升序)，第二个键保证"同分"也有确定顺序。
func (s *Set) Match(task string, limit int) []Match {
	q := strings.ToLower(strings.TrimSpace(task))
	if q == "" || len(s.skills) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = s.opts.MaxCandidates
	}

	out := make([]Match, 0, len(s.skills))
	for _, sk := range s.skills {
		if m, ok := scoreSkill(q, sk); ok {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Skill.Name < out[j].Skill.Name
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// scoreSkill 给单个技能打分；0 分表示不相关，不进入候选。
func scoreSkill(q string, sk *Skill) (Match, bool) {
	m := Match{Skill: sk}
	for _, term := range sk.Terms {
		if containsTerm(q, strings.ToLower(term)) {
			m.Score += weightWhenToUse
			m.Reasons = append(m.Reasons, fmt.Sprintf("when-to-use 命中 %q", term))
		}
	}

	// 名字命中（pdf-processing 里的 "pdf"）通常是强信号，算一次就够。
	lowerName := strings.ToLower(sk.Name)
	if containsTerm(q, lowerName) {
		m.Score += weightName
		m.Reasons = append(m.Reasons, fmt.Sprintf("name 命中 %q", sk.Name))
	} else if spaced := strings.ReplaceAll(lowerName, "-", " "); spaced != lowerName && containsTerm(q, spaced) {
		m.Score += weightName
		m.Reasons = append(m.Reasons, fmt.Sprintf("name 命中 %q", spaced))
	}

	hits := 0
	for _, term := range describeTerms(sk.Description) {
		if hits >= maxDescHits {
			break
		}
		if containsTerm(q, term) {
			hits++
			m.Score += weightDesc
			m.Reasons = append(m.Reasons, fmt.Sprintf("description 命中 %q", term))
		}
	}
	return m, m.Score > 0
}

// containsTerm 判断任务文本里是否出现某个词。
//
// 分两条路：中文按子串匹配（中文没有空格分词，子串是最稳的近似），
// 英文按词边界匹配（否则 "test" 会命中 "contest"，把无关任务也拉进来）。
func containsTerm(hay, term string) bool {
	term = strings.TrimSpace(strings.ToLower(term))
	if term == "" || hay == "" {
		return false
	}
	if !isASCII(term) {
		return strings.Contains(hay, term)
	}
	start := 0
	for {
		idx := strings.Index(hay[start:], term)
		if idx < 0 {
			return false
		}
		from := start + idx
		to := from + len(term)
		if isBoundary(hay, from, true) && isBoundary(hay, to, false) {
			return true
		}
		start = from + 1
	}
}

// isBoundary 判断位置 pos 前面（before=true）或后面是否是词边界。
// 用 DecodeLastRune/DecodeRune 而不是直接取字节，避免把多字节字符的半个字节
// 当成 ASCII 字母。
func isBoundary(s string, pos int, before bool) bool {
	if before {
		if pos == 0 {
			return true
		}
		r, _ := utf8.DecodeLastRuneInString(s[:pos])
		return !isWordRune(r)
	}
	if pos >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[pos:])
	return !isWordRune(r)
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// isCJK 判断是否中日韩表意字符。够用即可：技能词表基本都是中英文。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

// englishStopwords 是描述里高频但没有区分度的英文词。
// 不滤掉它们，"for/the/with" 这类词会把完全不相关的任务也匹配上。
var englishStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "use": true, "when": true,
	"this": true, "that": true, "you": true, "your": true, "are": true, "not": true,
	"can": true, "all": true, "any": true, "from": true, "into": true, "its": true,
	"but": true, "has": true, "have": true, "will": true, "should": true, "must": true,
	// "task" 几乎出现在每条技能描述里，作为匹配词没有任何区分度。
	"task": true,
}

// describeTerms 从描述里抽取可用于匹配的词。
//
// 英文取长度 >=3 的单词；中文取连续汉字串的二元组（"测试覆盖" → "测试""试覆""覆盖"）。
// 二元组当然不如分词器准，但它不引入任何依赖、不会因为词典缺失而崩，
// 对"描述里有没有提到这个主题"这个粗糙判断已经足够。
func describeTerms(desc string) []string {
	if desc == "" {
		return nil
	}
	seen := make(map[string]bool, 16)
	out := make([]string, 0, 16)
	add := func(term string) {
		if term == "" || seen[term] || len(out) >= maxDescTerms || englishStopwords[term] {
			return
		}
		seen[term] = true
		out = append(out, term)
	}

	var ascii []rune
	var cjk []rune
	flushASCII := func() {
		if len(ascii) >= 3 {
			add(string(ascii))
		}
		ascii = ascii[:0]
	}
	flushCJK := func() {
		for i := 0; i+1 < len(cjk); i++ {
			add(string(cjk[i : i+2]))
		}
		cjk = cjk[:0]
	}

	for _, r := range strings.ToLower(desc) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			flushCJK()
			ascii = append(ascii, r)
		case isCJK(r):
			flushASCII()
			cjk = append(cjk, r)
		default:
			flushASCII()
			flushCJK()
		}
	}
	flushASCII()
	flushCJK()
	return out
}
