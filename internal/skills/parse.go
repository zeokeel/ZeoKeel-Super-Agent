package skills

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// skillFileName 是技能正文的固定文件名。约定 <root>/<name>/SKILL.md。
const skillFileName = "SKILL.md"

const (
	maxNameLen  = 64
	maxDescRune = 500
)

// namePattern 只允许 kebab-case。
//
// 为什么这么严：name 同时是目录名、是系统提示里的标识、将来还是 skill 工具的参数。
// 允许空格/斜杠/点会立刻带来"同一个技能多种写法"和路径拼接问题。
var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// whenToUseSep 是 when-to-use 的分隔符集合：中英文逗号、顿号、分号、竖线、换行。
const whenToUseSep = ",，、;；|\n"

// parseSkill 解析并校验一个 SKILL.md。
//
// 校验分两类：结构（front matter 能不能解析）与语义（必填字段、命名、大小）。
// 两类都用同一种方式报错——带上文件路径和字段名，因为人改 SKILL.md 时最需要知道
// "哪个文件的哪个字段错了"。
func parseSkill(dirName, path string, raw []byte, opts Options) (*Skill, error) {
	if len(raw) > opts.MaxSkillBytes {
		return nil, fmt.Errorf("skills: %s 太大（%d 字节，上限 %d）", path, len(raw), opts.MaxSkillBytes)
	}
	text := normalizeText(string(raw))
	meta, body, err := splitFrontMatter(text)
	if err != nil {
		return nil, fmt.Errorf("skills: %s: %w", path, err)
	}

	name := strings.TrimSpace(metaField(meta, "name"))
	if name == "" {
		return nil, fmt.Errorf("skills: %s: front matter 缺少必填字段 name", path)
	}
	if len(name) > maxNameLen {
		return nil, fmt.Errorf("skills: %s: name %q 超过 %d 字符", path, name, maxNameLen)
	}
	if !namePattern.MatchString(name) {
		return nil, fmt.Errorf("skills: %s: name %q 不是 kebab-case（只允许小写字母、数字、单个连字符）", path, name)
	}
	// name 必须等于目录名：两份真相（目录名/字段名）一旦不一致，就会出现
	// "按名字找技能"和"按目录看技能"结果不同的诡异问题。
	if dirName != "" && name != dirName {
		return nil, fmt.Errorf("skills: %s: name %q 与目录名 %q 不一致", path, name, dirName)
	}

	desc := strings.TrimSpace(metaField(meta, "description"))
	if desc == "" {
		return nil, fmt.Errorf("skills: %s: front matter 缺少必填字段 description", path)
	}
	if utf8.RuneCountInString(desc) > maxDescRune {
		return nil, fmt.Errorf("skills: %s: description 超过 %d 字符", path, maxDescRune)
	}

	whenUse := strings.TrimSpace(metaField(meta, "when-to-use", "when_to_use", "whentouse"))
	if whenUse == "" {
		return nil, fmt.Errorf("skills: %s: front matter 缺少必填字段 when-to-use", path)
	}
	terms := splitTerms(whenUse)
	if len(terms) == 0 {
		return nil, fmt.Errorf("skills: %s: when-to-use 里没有可用的关键词（太短或全为空）", path)
	}

	if body == "" {
		return nil, fmt.Errorf("skills: %s: 正文为空（front matter 之后必须有内容）", path)
	}

	return &Skill{
		Name:         name,
		Description:  desc,
		WhenToUse:    whenUse,
		WhenNotToUse: strings.TrimSpace(metaField(meta, "when-not-to-use", "when-not-use", "when_not_to_use")),
		Terms:        terms,
		Body:         body,
		Source:       path,
		Bytes:        len(raw),
	}, nil
}

// normalizeText 统一换行并把 BOM 去掉。
// Windows 上写出来的 SKILL.md 是 CRLF，不统一会让 front matter 的 "---" 判定失败。
func normalizeText(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// splitFrontMatter 把文件拆成元数据表与正文。
//
// 只支持扁平的 `key: value`，不引入 YAML 解析器。理由：技能元数据就是三个短字段，
// 而 YAML 会把 `description: on` 解析成 bool、把带冒号的描述解析成 map——这些
// "聪明"的行为在配置里只会变成难查的坑。遇到不认识的写法直接报错，人改一行就好。
func splitFrontMatter(text string) (map[string]string, string, error) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, "", errors.New("缺少 front matter：文件第一行必须是 ---")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, "", errors.New("front matter 没有结束标记 ---")
	}

	meta := make(map[string]string, 8)
	for i := 1; i < end; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return nil, "", fmt.Errorf("front matter 第 %d 行不是 key: value 形式: %q", i+1, line)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			return nil, "", fmt.Errorf("front matter 第 %d 行字段名为空: %q", i+1, line)
		}
		if _, dup := meta[key]; dup {
			return nil, "", fmt.Errorf("front matter 字段 %q 重复", key)
		}
		v, err := unquote(strings.TrimSpace(val))
		if err != nil {
			return nil, "", fmt.Errorf("front matter 字段 %q: %w", key, err)
		}
		meta[key] = v
	}
	body := strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	return meta, body, nil
}

// metaField 按顺序找第一个非空字段，用来兼容少量别名写法。
//
// 只兼容 when-to-use 的几种常见写法（下划线/驼峰），不兼容 name/description：
// 那两个字段拼错时，"缺少必填字段"的报错已经足够清楚。
func metaField(meta map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := meta[k]; ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// unquote 去掉成对的引号。
// 人写描述时常带冒号（"Use when: you need X"），加引号能让它整行被当成一个值。
func unquote(s string) (string, error) {
	if len(s) < 2 {
		return s, nil
	}
	switch s[0] {
	case '"':
		if s[len(s)-1] != '"' {
			return "", errors.New("双引号没有闭合")
		}
		if v, err := strconv.Unquote(s); err == nil {
			return v, nil
		}
		return s[1 : len(s)-1], nil
	case '\'':
		if s[len(s)-1] != '\'' {
			return "", errors.New("单引号没有闭合")
		}
		return s[1 : len(s)-1], nil
	}
	return s, nil
}

// splitTerms 把 when-to-use 拆成关键词。
//
// 丢掉少于 2 个字符的词：单字（"的""a"）几乎必然命中任何任务，等于把技能变成
// 永远注入，反而稀释真正的匹配信号。
func splitTerms(raw string) []string {
	seen := make(map[string]bool, 8)
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return strings.ContainsRune(whenToUseSep, r)
	}) {
		term := strings.TrimSpace(part)
		if utf8.RuneCountInString(term) < 2 {
			continue
		}
		key := strings.ToLower(term)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, term)
	}
	return out
}
