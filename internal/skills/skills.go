// Package skills 从磁盘发现 SKILL.md 技能，并把选中的技能注入系统提示。
//
// 位置（design §5.3/§5.10）：skills 属于"组织与扩展"组里的通用能力，worker 与
// orchestrator 共享（两者的工具白名单都含 skill）。本包不碰模型调用，也不决定
// 提示词的整体形状——它只回答两个问题："磁盘上有哪些技能"和"这次任务该注入哪些"。
//
// 一个刻意的设计：技能选择是确定性的（关键词/描述匹配），不调模型。
//
//  1. 可复现。同一个 Ticket 重跑必须得到同一组技能，否则长任务复盘时无法解释
//     "为什么这轮用了 PDF 技能、上一轮没用"。
//  2. 便宜。拼系统提示发生在每次模型调用之前，如果为选技能再加一次模型调用，
//     延迟与成本会随轮次线性增长。
//  3. 可审计。面板可以直接展示"命中了哪个关键词"，而模型路由只能给出黑盒结果。
//
// 代价是可能漏掉语义相关但字面不匹配的技能。这个洞用"模型可以主动调用 skill 工具
// 按名字加载"来补：注入是"顺手提供"，不是"唯一入口"。
package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 默认预算。数值是按"系统提示总预算 16KB 上下"反推的：
// 单个技能正文超过 16KB 就不该塞进提示，而应该让模型用 read 工具读。
const (
	defaultMaxSkillBytes  = 64 * 1024
	defaultMaxSkills      = 128
	defaultMaxPromptBytes = 16 * 1024
	defaultMaxCandidates  = 3
)

// Options 控制加载与渲染的预算。零值即用默认值。
type Options struct {
	// MaxSkillBytes 是单个 SKILL.md 的上限，超了报错（多半是误放了长文档）。
	MaxSkillBytes int
	// MaxSkills 是技能数量上限，防止把整棵仓库当技能目录扫。
	MaxSkills int
	// MaxPromptBytes 是渲染结果的总预算，超了就截断后面的正文。
	MaxPromptBytes int
	// MaxCandidates 是默认注入的技能数；prompt 里塞太多技能会稀释注意力。
	MaxCandidates int
}

func (o Options) withDefaults() Options {
	if o.MaxSkillBytes <= 0 {
		o.MaxSkillBytes = defaultMaxSkillBytes
	}
	if o.MaxSkills <= 0 {
		o.MaxSkills = defaultMaxSkills
	}
	if o.MaxPromptBytes <= 0 {
		o.MaxPromptBytes = defaultMaxPromptBytes
	}
	if o.MaxCandidates <= 0 {
		o.MaxCandidates = defaultMaxCandidates
	}
	return o
}

// Skill 是一个已加载并通过校验的技能。
type Skill struct {
	Name        string
	Description string
	// WhenToUse/WhenNotToUse 是原文，渲染时给人（和模型）看。
	WhenToUse    string
	WhenNotToUse string
	// Terms 是从 when-to-use 拆出来的匹配词。
	Terms []string
	// Body 是 front matter 之后的正文。
	Body string
	// Source 是 SKILL.md 的绝对路径，落盘/面板定位用。
	Source string
	// Bytes 是文件大小，便于面板提示"这个技能很大"。
	Bytes int
}

// Set 是一次加载的结果。Set 创建后只读，可并发被多个 goroutine 使用。
type Set struct {
	opts   Options
	skills []*Skill
	byName map[string]*Skill
	roots  []string
}

// Load 加载单个 skills 根目录下的全部技能。
//
// 严格失败而不是跳过坏技能：一个 front matter 写错的技能如果被静默忽略，
// 模型就永远拿不到它，而人完全不会发现——直到某次任务表现异常。宁可启动时报错。
func Load(root string, opts Options) (*Set, error) {
	return LoadDirs([]string{root}, opts)
}

// LoadDirs 加载多个根目录（例如内置技能 + 用户技能）。
//
// 重名直接报错而不是"后者覆盖前者"：覆盖会让"到底加载了哪个"取决于目录顺序，
// 排查时极难发现。
func LoadDirs(roots []string, opts Options) (*Set, error) {
	opts = opts.withDefaults()
	if len(roots) == 0 {
		return nil, errors.New("skills: 没有提供任何根目录")
	}
	set := &Set{opts: opts, byName: map[string]*Skill{}}
	var errs []error
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			errs = append(errs, errors.New("skills: 根目录为空"))
			continue
		}
		found, err := loadRoot(root, opts)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, sk := range found {
			if prev, ok := set.byName[sk.Name]; ok {
				errs = append(errs, fmt.Errorf("skills: 技能 %q 重名：%s 与 %s", sk.Name, prev.Source, sk.Source))
				continue
			}
			set.byName[sk.Name] = sk
			set.skills = append(set.skills, sk)
		}
		set.roots = append(set.roots, root)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	sort.Slice(set.skills, func(i, j int) bool { return set.skills[i].Name < set.skills[j].Name })
	return set, nil
}

// loadRoot 扫描一个根目录。约定：<root>/<name>/SKILL.md。
//
// 所有读取都经过 os.Root（Go 1.24+）：它在系统调用层面拒绝任何解析到根目录之外的
// 路径（符号链接也一样），因此"检查路径是否在根内"与"真正读取"之间没有 TOCTOU
// 窗口。旧实现用 Lstat + EvalSymlinks + filepath.Rel 事后校验，能读到一半再拒绝——
// 这个包防的是提示注入（把任意文件变成系统提示的一部分），必须在内核层面拦。
func loadRoot(root string, opts Options) ([]*Skill, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("skills: 解析根目录 %q: %w", root, err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("skills: 根目录 %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("skills: 根目录 %q 不是目录", root)
	}
	r, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, fmt.Errorf("skills: 打开根目录 %q: %w", root, err)
	}
	defer r.Close()
	entries, err := fs.ReadDir(r.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("skills: 读取根目录 %q: %w", root, err)
	}

	var skills []*Skill
	var errs []error
	// seenDirs 按"解析后真实路径"去重：根目录内的符号链接若指向同一个技能目录，
	// 它是同一个技能的两个入口，不该被当成两次加载（否则会撞"重名"）。去重键只是
	// 为了"一个真实目录算一个技能"，安全边界由 os.Root 负责。
	seenDirs := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		// 隐藏目录（.git/.zeokeel）和"下划线开头"的约定目录不是技能。
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		dirRel, canonical, err := resolveSkillDir(r, absRoot, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if dirRel == "" || seenDirs[canonical] {
			continue
		}
		seenDirs[canonical] = true
		path := filepath.Join(absRoot, name, skillFileName)
		raw, err := readSkillBody(r, absRoot, dirRel, path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // 没有 SKILL.md 的目录不是技能目录
			}
			errs = append(errs, err)
			continue
		}
		// dirName 用解析后的真实目录名：alias -> real 的符号链接应当被当成 real，
		// 否则同一个技能会因为入口不同而报"name 与目录名不一致"。
		sk, err := parseSkill(filepath.Base(canonical), path, raw, opts)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		skills = append(skills, sk)
		if len(skills) > opts.MaxSkills {
			errs = append(errs, fmt.Errorf("skills: 技能数量超过上限 %d（根目录 %q）", opts.MaxSkills, root))
			break
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return skills, nil
}

// resolveSkillDir 判断一个条目是不是"根目录内的目录"，返回读取用的根内相对路径
// 和去重用的真实路径。
//
// 返回 ("", "", nil) 表示"不是目录，跳过"。返回错误表示"是目录但越界"——越界必须
// 报错而不是静默跳过，跳过会让人以为技能已加载。
func resolveSkillDir(r *os.Root, absRoot, name string) (dirRel, canonical string, err error) {
	full := filepath.Join(absRoot, name)
	rel, err := mapSymlink(r, absRoot, name)
	if err != nil {
		if isEscape(err) {
			return "", "", fmt.Errorf("skills: 技能目录 %q 通过符号链接逃出了根目录: %w", name, err)
		}
		return "", "", fmt.Errorf("skills: 检查 %q: %w", full, err)
	}
	info, err := r.Stat(rel)
	if err != nil {
		if isEscape(err) {
			return "", "", fmt.Errorf("skills: 技能目录 %q 通过符号链接逃出了根目录: %w", name, err)
		}
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", nil // 断链或列目录后被删除：不是技能目录
		}
		return "", "", fmt.Errorf("skills: 检查 %q: %w", full, err)
	}
	if !info.IsDir() {
		return "", "", nil
	}
	// 去重键：EvalSymlinks 只为"alias 与 real 算一个技能"，不是安全校验——后续
	// 读取仍然经过 os.Root，即使它解析到根外也读不到。
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		canonical = resolved
	} else {
		canonical = filepath.Join(absRoot, rel)
	}
	return rel, canonical, nil
}

// readSkillBody reads <dir>/SKILL.md through the root. The file itself may be a
// symlink; an absolute one that points inside the root is mapped to its relative
// path (os.Root refuses absolute links even when they stay inside), and one that
// resolves outside is refused with the message the panel has always shown.
func readSkillBody(r *os.Root, absRoot, dirRel, displayPath string) ([]byte, error) {
	rel, err := mapSymlink(r, absRoot, filepath.Join(dirRel, skillFileName))
	if err != nil {
		if isEscape(err) {
			return nil, fmt.Errorf("skills: %s 是指向根目录外的符号链接: %w", displayPath, err)
		}
		return nil, err
	}
	raw, err := r.ReadFile(rel)
	if err != nil {
		if isEscape(err) {
			return nil, fmt.Errorf("skills: %s 是指向根目录外的符号链接: %w", displayPath, err)
		}
		return nil, fmt.Errorf("skills: 读取 %q: %w", displayPath, err)
	}
	return raw, nil
}

// mapSymlink maps a root-relative name to the root-relative path reads should use.
//
// os.Root follows relative symlinks inside the root by itself, but refuses
// absolute ones even when they point inside ("symbolic links must not be
// absolute" in the os.Root docs). An absolute link that resolves inside the root is
// therefore translated into its relative path here; one that resolves outside is
// an error. The translated path is still read through os.Root, so a link retargeted
// after this check cannot escape either.
func mapSymlink(r *os.Root, absRoot, relName string) (string, error) {
	li, err := r.Lstat(relName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return relName, nil // let the caller's ReadFile/Stat report it
		}
		return "", err
	}
	if li.Mode()&os.ModeSymlink == 0 {
		return relName, nil
	}
	target, err := r.Readlink(relName)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		// A relative link is resolved by os.Root itself; hand it the joined path so
		// an escape is refused at the syscall.
		return filepath.Join(filepath.Dir(relName), target), nil
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", fmt.Errorf("无法解析符号链接目标 %q: %w", target, err)
	}
	if !insideRoot(absRoot, resolved) {
		return "", fmt.Errorf("%w: %q 解析到 %q，不在根目录 %q 之内", errSymlinkEscape, relName, resolved, absRoot)
	}
	rel, err := filepath.Rel(absRoot, resolved)
	if err != nil {
		return "", err
	}
	return rel, nil
}

// insideRoot reports whether path is absRoot or below it, lexically. It is only
// used to explain an escape; reads never depend on it.
func insideRoot(absRoot, path string) bool {
	rel, err := filepath.Rel(absRoot, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// errSymlinkEscape marks a symlink this package resolved itself and found outside
// the root (os.Root never sees an absolute link, so its own sentinel would not fire).
var errSymlinkEscape = errors.New("symbolic link escapes the skills root")

// isEscape reports whether err means "a path resolved outside the skills root":
// either os.Root's own sentinel (unexported: os.errPathEscapes, matched by message)
// or a symlink this package resolved. It only exists to translate the error into
// the Chinese wording the panel shows.
func isEscape(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errSymlinkEscape) ||
		strings.Contains(err.Error(), "path escapes from parent")
}

// All 返回按名字排序的全部技能（副本，避免调用方改到内部切片）。
func (s *Set) All() []*Skill {
	out := make([]*Skill, len(s.skills))
	copy(out, s.skills)
	return out
}

// Len 返回技能数量。
func (s *Set) Len() int { return len(s.skills) }

// Names 返回按名字排序的技能名。
func (s *Set) Names() []string {
	out := make([]string, 0, len(s.skills))
	for _, sk := range s.skills {
		out = append(out, sk.Name)
	}
	return out
}

// Roots 返回参与加载的根目录（加载失败的那些不会出现在这里）。
func (s *Set) Roots() []string {
	out := make([]string, len(s.roots))
	copy(out, s.roots)
	return out
}

// Get 按名字取技能。
func (s *Set) Get(name string) (*Skill, bool) {
	sk, ok := s.byName[name]
	return sk, ok
}
