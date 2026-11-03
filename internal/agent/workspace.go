package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	fsmiddleware "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// 文件工具的说明保持与迁移前一致：工具名和描述是对模型的承诺，也是提示词里
// "read/grep/glob" 这些引用的锚点。official filesystem 中间件允许用
// ToolConfig.Name/Desc 覆盖，所以迁移不改模型看到的工具名与措辞。
const (
	readToolDesc = "Read a text file from the workspace, optionally a range of lines. " +
		"Use it to confirm what a file really contains before editing it; do not assume."
	writeToolDesc = "Create or replace a text file in the workspace. Paths are relative to the workspace root; " +
		"paths outside it are refused as a result you can read. Parent directories are created."
	editToolDesc = "Replace one exact occurrence of old_string with new_string in a file. " +
		"old_string must match exactly once; read the file first and include enough surrounding text to be unique."
	globToolDesc = "List files in the workspace matching a glob pattern, newest first. " +
		"Use it to find where something lives before reading it."
	grepToolDesc = "Search file contents with a regular expression. " +
		"Set output_mode=content to see matching lines and their line numbers."
)

// fileToolNames 是 5 个文件/搜索工具的名字与固定顺序。顺序必须稳定：工具列表是
// 提示词缓存前缀的一部分（cache_test.go 断言跨轮工具顺序不变）。
var fileToolNames = []string{"read", "write", "edit", "glob", "grep"}

// agentMetadataDirs 是 agent 自己写的工作区目录：会话事件日志、落盘的工具输出、
// 命令临时文件。遍历类工具（grep/glob）必须跳过它们，否则会出现自引用：grep 的
// 参数先被写进 .zeokeel/sessions/*.evlog，随后 grep 扫到这份日志、匹配到自己——
// 一个"找不到这个字符串"的查询会返回命中。read 仍然可以显式读取这些路径（
// reduction 的提示就是让模型用 read 打开 .zeokeel/tool-output 下的完整输出）。
var agentMetadataDirs = []string{".zeokeel", ".tmp"}

// isAgentMetadataPath reports whether a workspace-relative path lives under an
// agent-owned metadata directory and therefore must not be traversed.
func isAgentMetadataPath(name string) bool {
	name = strings.TrimPrefix(filepath.ToSlash(name), "./")
	for _, dir := range agentMetadataDirs {
		if name == dir || strings.HasPrefix(name, dir+"/") {
			return true
		}
	}
	return false
}

// refusalError 标记"工具级问题"：路径越界、edit 命中多次之类。它应当在
// WrapInvokableToolCall 里被翻成模型可读的 observation，而不是让整轮失败
// （design §5.5/§5.8；eino 的 ToolsNode 会把工具 error 当整轮失败）。
type refusalError struct{ msg string }

func (e *refusalError) Error() string { return e.msg }

func refuse(format string, args ...any) error {
	return &refusalError{msg: fmt.Sprintf(format, args...)}
}

// pathRefusal 把"模型给了个不能用的路径"这类错误标成 refusal。只有 I/O 层面的
// 基础设施错误（磁盘坏、写满）才会继续上抛并终止整轮。
func pathRefusal(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) ||
		errors.Is(err, fs.ErrInvalid) || strings.Contains(err.Error(), "is a directory") {
		return refuse("%s: %v", op, err)
	}
	return err
}

// toolErrorObservation 把可恢复的工具错误翻成 observation；第二个返回值为 false
// 表示这是基础设施错误（磁盘、provider），应当继续上抛。
func toolErrorObservation(err error) (string, bool) {
	var refusal *refusalError
	if errors.As(err, &refusal) {
		return refusal.msg, true
	}
	// os.Root 的越界符号链接错误没有导出符号（os/root.go: errPathEscapes），
	// 错误文本是唯一稳定的判别方式；它只出现在工具路径参数越界这一种情况。
	if strings.Contains(err.Error(), "path escapes from parent") {
		return "path escapes the workspace (symlinks may not point outside it)", true
	}
	// 不存在的文件/目录与权限问题都是模型可以换个路径重试的 observation；
	// 磁盘满、I/O 错误等仍按基础设施错误处理。
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return err.Error(), true
	}
	return "", false
}

// toolRefusalMiddleware 把文件工具的可恢复错误翻成文本 observation。
//
// eino 的 compose.ToolsNode 把任何工具 error 当整轮失败（compose/tool_node.go
// Invoke：tasks[i].err != nil 直接返回 error），而设计要求"一次差 1 行的 read、
// 一个越界路径"只是模型能看到并自我纠正的观察。官方 filesystem 工具以 error
// 表达这类问题，所以这里用官方的 WrapInvokableToolCall 钩子把它们改成结果。
type toolRefusalMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
}

func newToolRefusalMiddleware() adk.ChatModelAgentMiddleware {
	return &toolRefusalMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
	}
}

func (m *toolRefusalMiddleware) WrapInvokableToolCall(_ context.Context,
	endpoint adk.InvokableToolCallEndpoint, _ *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		out, err := endpoint(ctx, argumentsInJSON, opts...)
		if err != nil {
			if msg, ok := toolErrorObservation(err); ok {
				return "refused: " + msg, nil
			}
		}
		return out, err
	}, nil
}

func (m *toolRefusalMiddleware) ChatModelAgentMiddlewareName() string { return "zeokeel-tool-refusal" }

// localBackend 是 filesystem.Backend 的实现：所有路径都从 os.Root 走，符号链接
// 不能逃出工作区（标准库保证，design §5.5 的目录约束）。目录遍历类操作（glob/
// grep）用 Root.FS() + doublestar，同样不越过 root。
type localBackend struct{ root string }

var _ filesystem.Backend = (*localBackend)(nil)

// rootPath 把模型给的路径解析成 root 内的本地路径（相对形式）。绝对路径只有在
// 工作区内时才接受：模型偶尔会把技能里写死的绝对路径原样带过来，直接拒绝会让它
// 反复犯错；工作区外的绝对路径仍然是 refusal。
func (b *localBackend) rootPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", refuse("path is required")
	}
	if filepath.IsAbs(name) {
		rel, err := filepath.Rel(b.root, filepath.Clean(name))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", refuse("%q is outside the workspace", name)
		}
		name = rel
	}
	clean := filepath.Clean(name)
	if !filepath.IsLocal(clean) {
		return "", refuse("%q is outside the workspace", name)
	}
	return clean, nil
}

func (b *localBackend) withRoot(fn func(*os.Root) error) error {
	root, err := os.OpenRoot(b.root)
	if err != nil {
		return fmt.Errorf("agent: open workspace root: %w", err)
	}
	defer root.Close()
	return fn(root)
}

func (b *localBackend) LsInfo(_ context.Context, req *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	if req == nil {
		return nil, refuse("ls: invalid request")
	}
	rel, err := b.rootPath(req.Path)
	if err != nil {
		return nil, err
	}
	var out []filesystem.FileInfo
	err = b.withRoot(func(root *os.Root) error {
		dir, err := root.Open(rel)
		if err != nil {
			return pathRefusal("cannot list "+req.Path, err)
		}
		entries, err := dir.ReadDir(-1)
		_ = dir.Close()
		if err != nil {
			return pathRefusal("cannot list "+req.Path, err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, fileInfo(filepath.Join(rel, e.Name()), info))
		}
		return nil
	})
	return out, err
}

// Read 实现 filesystem.Backend.Read。offset/limit 是官方 read_file 的行语义：
// offset 1-based，limit<=0 表示读到结尾；越界不是错误，返回空内容（官方工具会把它
// 变成"文件为空或 offset 越过末行"的文本，而不是整轮失败）。
func (b *localBackend) Read(_ context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	if req == nil {
		return nil, refuse("read: invalid request")
	}
	rel, err := b.rootPath(req.FilePath)
	if err != nil {
		return nil, err
	}
	var data []byte
	err = b.withRoot(func(root *os.Root) error {
		var readErr error
		data, readErr = root.ReadFile(rel)
		return pathRefusal("cannot read "+req.FilePath, readErr)
	})
	if err != nil {
		return nil, err
	}
	return &filesystem.FileContent{Content: sliceLines(string(data), req.Offset, req.Limit)}, nil
}

// Write 实现 filesystem.Backend.Write，父目录按需创建。
func (b *localBackend) Write(_ context.Context, req *filesystem.WriteRequest) error {
	if req == nil {
		return refuse("write: invalid request")
	}
	rel, err := b.rootPath(req.FilePath)
	if err != nil {
		return err
	}
	return b.withRoot(func(root *os.Root) error {
		if dir := filepath.Dir(rel); dir != "." && dir != "" {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return pathRefusal("cannot create directory for "+req.FilePath, err)
			}
		}
		return pathRefusal("cannot write "+req.FilePath, root.WriteFile(rel, []byte(req.Content), 0o644))
	})
}

// Edit 实现 filesystem.Backend.Edit。命中多次是 refusal：告诉模型"把上下文写长一点
// 再试"，而不是让整轮失败（design §5.5）。
func (b *localBackend) Edit(_ context.Context, req *filesystem.EditRequest) error {
	if req == nil {
		return refuse("edit: invalid request")
	}
	rel, err := b.rootPath(req.FilePath)
	if err != nil {
		return err
	}
	if req.OldString == "" {
		return refuse("old_string is required")
	}
	if req.OldString == req.NewString {
		return refuse("new_string must differ from old_string")
	}
	var next string
	err = b.withRoot(func(root *os.Root) error {
		data, readErr := root.ReadFile(rel)
		if readErr != nil {
			return pathRefusal("cannot read "+req.FilePath, readErr)
		}
		body := string(data)
		switch count := strings.Count(body, req.OldString); {
		case count == 0:
			return refuse("old_string not found in %s; read the file again before editing", req.FilePath)
		case count > 1 && !req.ReplaceAll:
			return refuse("old_string appears %d times in %s; include more surrounding text to make it unique, "+
				"or set replace_all=true to replace every occurrence", count, req.FilePath)
		case req.ReplaceAll:
			next = strings.ReplaceAll(body, req.OldString, req.NewString)
		default:
			next = strings.Replace(body, req.OldString, req.NewString, 1)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return b.withRoot(func(root *os.Root) error {
		return pathRefusal("cannot write "+req.FilePath, root.WriteFile(rel, []byte(next), 0o644))
	})
}

// GlobInfo 实现 filesystem.Backend.GlobInfo：doublestar 的 ** 语义（跨目录），
// 在 Root.FS() 上匹配，因此结果天然不越界。结果按修改时间倒序，兑现描述里的
// "newest first"（旧实现按字母序，描述与实现已经漂移，见 F2）。
func (b *localBackend) GlobInfo(_ context.Context, req *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	if req == nil || strings.TrimSpace(req.Pattern) == "" {
		return nil, refuse("pattern is required")
	}
	pattern := strings.TrimSpace(req.Pattern)
	if filepath.IsAbs(pattern) {
		rel, err := b.rootPath(pattern)
		if err != nil {
			return nil, err
		}
		pattern = filepath.ToSlash(rel)
	}
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")
	if base := strings.TrimSpace(req.Path); base != "" && base != "." {
		relBase, err := b.rootPath(base)
		if err != nil {
			return nil, err
		}
		pattern = path.Join(filepath.ToSlash(relBase), pattern)
	}
	if _, err := doublestar.Match(pattern, ""); err != nil {
		return nil, refuse("invalid glob pattern %q: %v", req.Pattern, err)
	}
	type entry struct {
		info filesystem.FileInfo
		mod  time.Time
	}
	var found []entry
	err := b.withRoot(func(root *os.Root) error {
		matches, err := doublestar.Glob(root.FS(), pattern, doublestar.WithFilesOnly())
		if err != nil {
			return refuse("invalid glob pattern %q: %v", req.Pattern, err)
		}
		for _, m := range matches {
			if isAgentMetadataPath(m) {
				continue
			}
			info, statErr := root.Stat(filepath.FromSlash(m))
			if statErr != nil {
				continue
			}
			found = append(found, entry{info: fileInfo(filepath.FromSlash(m), info), mod: info.ModTime()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].mod.Equal(found[j].mod) {
			return found[i].info.Path < found[j].info.Path
		}
		return found[i].mod.After(found[j].mod)
	})
	out := make([]filesystem.FileInfo, 0, len(found))
	for _, e := range found {
		out = append(out, e.info)
	}
	return out, nil
}

// GrepRaw 实现 filesystem.Backend.GrepRaw。用 Go 的 regexp + Root.FS 遍历，不再
// 依赖外部 grep 二进制（Windows 上没有它，见 F5），也不越界。
func (b *localBackend) GrepRaw(_ context.Context, req *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	if req == nil || req.Pattern == "" {
		return nil, refuse("pattern is required")
	}
	pattern := req.Pattern
	if req.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	if req.EnableMultiline {
		pattern = "(?s)" + pattern
	}
	expr, err := regexp.Compile(pattern)
	if err != nil {
		return nil, refuse("invalid regular expression %q: %v", req.Pattern, err)
	}
	base := "."
	if strings.TrimSpace(req.Path) != "" {
		rel, err := b.rootPath(req.Path)
		if err != nil {
			return nil, err
		}
		base = filepath.ToSlash(rel)
	}
	var matches []filesystem.GrepMatch
	err = b.withRoot(func(root *os.Root) error {
		rootFS := root.FS()
		return fs.WalkDir(rootFS, base, func(name string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if isAgentMetadataPath(name) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if req.Glob != "" {
				ok, matchErr := doublestar.Match(req.Glob, name)
				if matchErr != nil {
					return matchErr
				}
				if !ok {
					ok, _ = doublestar.Match(req.Glob, path.Base(name))
				}
				if !ok {
					return nil
				}
			}
			if req.FileType != "" && !fileMatchesType(name, req.FileType) {
				return nil
			}
			data, readErr := fs.ReadFile(rootFS, name)
			if readErr != nil {
				return nil
			}
			if isBinary(data) {
				return nil
			}
			rel := filepath.ToSlash(name)
			if req.EnableMultiline {
				loc := expr.FindStringIndex(string(data))
				if loc == nil {
					return nil
				}
				line := 1 + strings.Count(string(data[:loc[0]]), "\n")
				matches = append(matches, filesystem.GrepMatch{
					Content: string(data[loc[0]:loc[1]]), Path: rel, Line: line,
				})
				return nil
			}
			for i, line := range strings.Split(string(data), "\n") {
				if expr.MatchString(line) {
					matches = append(matches, filesystem.GrepMatch{Content: line, Path: rel, Line: i + 1})
					if len(matches) >= 2000 {
						return fs.SkipAll
					}
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// fileInfo 把 os.FileInfo 翻成 eino 的 FileInfo；路径统一用 / 分隔，和 Root.FS()
// 的遍历结果保持一致。
func fileInfo(name string, info os.FileInfo) filesystem.FileInfo {
	return filesystem.FileInfo{
		Path:       filepath.ToSlash(name),
		IsDir:      info.IsDir(),
		Size:       info.Size(),
		ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
	}
}

// sliceLines 取 1-based 行区间的文本；offset 越过末行返回空串（官方 read_file
// 依赖这个约定给出"文件为空或 offset 越界"的提示，而不是让整轮失败）。
func sliceLines(content string, offset, limit int) string {
	if offset < 1 {
		offset = 1
	}
	start := 0
	for i := 1; i < offset; i++ {
		idx := strings.IndexByte(content[start:], '\n')
		if idx < 0 {
			return ""
		}
		start += idx + 1
	}
	if start >= len(content) {
		return ""
	}
	rest := content[start:]
	if limit <= 0 {
		return strings.TrimSuffix(rest, "\n")
	}
	end := 0
	for i := 0; i < limit; i++ {
		idx := strings.IndexByte(rest[end:], '\n')
		if idx < 0 {
			end = len(rest)
			break
		}
		end += idx + 1
	}
	return strings.TrimSuffix(rest[:end], "\n")
}

func isBinary(data []byte) bool {
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	for _, b := range probe {
		if b == 0 {
			return true
		}
	}
	return false
}

// fileMatchesType 支持常见扩展名过滤；不认识的类型不匹配任何文件（与 rg --type
// 的未知类型行为一致，但这里不报错，避免模型因为没有 type 定义而整轮失败）。
func fileMatchesType(name, fileType string) bool {
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	switch strings.ToLower(fileType) {
	case "go":
		return ext == "go"
	case "js":
		return ext == "js" || ext == "jsx" || ext == "mjs" || ext == "cjs"
	case "ts":
		return ext == "ts" || ext == "tsx"
	case "py":
		return ext == "py"
	case "rust":
		return ext == "rs"
	case "java":
		return ext == "java"
	case "json":
		return ext == "json"
	case "md":
		return ext == "md"
	case "yaml", "yml":
		return ext == "yaml" || ext == "yml"
	default:
		return ext == strings.ToLower(fileType)
	}
}

// newFilesystemMiddleware 用 eino 官方 filesystem 中间件注册文件工具。enabled 为
// nil 表示全部注册；otherwise 只注册 map 里为 true 的工具（子智能体白名单用）。
//
// 取舍：ChatModelAgentMiddleware 版本的 New 不做大结果落盘（那是废弃的
// NewMiddleware/AgentMiddleware 路径才有的 WrapToolCall），所以不存在与已有
// reduction 中间件双写的问题（F4 警告的场景只在使用旧中间件时成立）。
//
// CustomSystemPrompt 指向空串：官方默认会往 instruction 里追加一段工具说明，
// 本项目的提示词是产品资产（internal/prompts），不能让框架偷偷改。
func newFilesystemMiddleware(ctx context.Context, root string, enabled map[string]bool) (adk.ChatModelAgentMiddleware, error) {
	backend := &localBackend{root: root}
	toolConfig := func(name, desc string) *fsmiddleware.ToolConfig {
		if enabled != nil && !enabled[name] {
			return &fsmiddleware.ToolConfig{Disable: true}
		}
		d := desc
		return &fsmiddleware.ToolConfig{Name: name, Desc: &d}
	}
	noPrompt := ""
	return fsmiddleware.New(ctx, &fsmiddleware.MiddlewareConfig{
		Backend:             backend,
		LsToolConfig:        &fsmiddleware.ToolConfig{Disable: true},
		ReadFileToolConfig:  toolConfig("read", readToolDesc),
		WriteFileToolConfig: toolConfig("write", writeToolDesc),
		EditFileToolConfig:  toolConfig("edit", editToolDesc),
		GlobToolConfig:      toolConfig("glob", globToolDesc),
		GrepToolConfig:      toolConfig("grep", grepToolDesc),
		CustomSystemPrompt:  &noPrompt,
	})
}
