package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/sources"
)

// apiUser 是 GitHub 返回的账号。deleted 账号是 null，调用方必须容忍。
type apiUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// apiComment 是 Issue/PR 评论里我们用到的最小字段集。没用上的字段不声明：
// 让 JSON 解码更宽容，GitHub 加字段不会影响我们。
type apiComment struct {
	ID       int64  `json:"id"`
	Body     string `json:"body"`
	HTMLURL  string `json:"html_url"`
	IssueURL string `json:"issue_url"`
	// PullRequestURL 是行内 review 评论（pulls/comments）指向 PR 的 API 地址。
	PullRequestURL string     `json:"pull_request_url"`
	User           *apiUser   `json:"user"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      *time.Time `json:"updated_at"`
}

// admission table 的行名（domain.Trigger.Row 的取值）。用单数，与 domain
// 注释和 002 的门禁表一致：GitHub 的行是 issue / pull / comment / ci。
const (
	RowComment = "comment"
	RowIssue   = "issue"
	RowPull    = "pull"
	RowCI      = "ci"
)

// Translate 把一条原始 GitHub 事件翻译成 domain.Trigger。
// 当前只翻译评论：通知用于决定读哪些 Thread，不会自己变成 Trigger。
func (s *Source) Translate(raw sources.Raw) (domain.Trigger, error) {
	switch raw.Kind {
	case RawComment:
		return s.commentTrigger(raw.Data, RawComment)
	case RawReviewComment:
		return s.commentTrigger(raw.Data, RawReviewComment)
	case RawIssue:
		return s.issueTrigger(raw.Data, false)
	case RawPull:
		return s.issueTrigger(raw.Data, true)
	case RawCI:
		return s.ciTrigger(raw.Data)
	default:
		return domain.Trigger{}, fmt.Errorf("github: unknown event kind %q", raw.Kind)
	}
}

// commentTrigger 做真正的映射。所有"事实字段"在这里一次定死，之后不再依赖
// 网络：谁发的、是不是人、有没有明确 @ agent、Thread 是哪个、回信地址在哪。
func (s *Source) commentTrigger(data []byte, kind string) (domain.Trigger, error) {
	var c apiComment
	if err := json.Unmarshal(data, &c); err != nil {
		return domain.Trigger{}, fmt.Errorf("github: unreadable comment: %w", err)
	}
	if c.ID == 0 {
		return domain.Trigger{}, errors.New("github: comment has no id")
	}
	if c.User == nil || strings.TrimSpace(c.User.Login) == "" {
		return domain.Trigger{}, errors.New("github: comment has no author")
	}
	// issue_url 与 html_url 都指向 Issue/PR；优先 issue_url，因为 PR 的评论
	// 在网页 URL 里可能是 /pull/12，而 API URL 始终是 /issues/12。
	repo, number, isPR, ok := ParseIssueRef(firstNonEmpty(c.IssueURL, c.PullRequestURL, c.HTMLURL))
	if !ok {
		return domain.Trigger{}, fmt.Errorf("github: comment %d has no issue or pull request", c.ID)
	}

	// 时间既进 At 也进 ID 的版本段：GitHub 允许编辑评论，编辑后 updated_at
	// 变化，应该被当成一条新事实重放；而同一份内容重读永远得到同一个 ID，
	// 所以崩溃重试不会重复建单（内容派生 ID 的全部意义，§4.4 不变量 1）。
	at := c.CreatedAt
	version := c.CreatedAt.UTC().Format(time.RFC3339Nano)
	if c.UpdatedAt != nil && !c.UpdatedAt.IsZero() {
		at = *c.UpdatedAt
		version = c.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	if at.IsZero() {
		return domain.Trigger{}, fmt.Errorf("github: comment %d has no timestamp", c.ID)
	}

	scope, workspace := s.triggerScope()
	threadID := ThreadID(repo, number)
	url := c.HTMLURL
	if url == "" {
		url = PageURL(repo, number, isPR)
	}

	tr := domain.Trigger{
		ID:      domain.Key(scope, "github-"+kind, repo, number, c.ID, version),
		Scope:   scope,
		Source:  string(Name),
		Threads: []domain.ThreadRef{{Source: string(Name), ID: threadID}},

		Actor: domain.Actor{ID: c.User.Login, Bot: IsBot(c.User.Login)},
		// Agent 自己的评论不是人发起的请求：收下它只会让 agent 自己叫自己。
		// 机器人同理（bot list 由 Policy 决定，Source 只交出事实）。
		Human: isHumanUser(*c.User) && !strings.EqualFold(c.User.Login, s.cfg.Login),
		Self:  s.isSelf(c.User.Login),
		// 只有明确 @ 到 agent 的 handle 才算 addressed。@所有人、@everyone、
		// 引用回复都不算（§4.9 两条容易踩的规则）。
		Addressed: domain.Addressed{Mention: MentionsAgent(c.Body, s.mention())},
		Row:       RowComment,
		// 打开了"接收评论/接收全部"时，这一行才收未 @ 的消息；这是 Source 的
		// 事实，gate 用它解锁 admission table 的两格（002:71）。
		ScopeReceivesAll: s.cfg.ObserveComments || s.cfg.ReceiveAll,

		Conversation: domain.Conversation{Kind: domain.ConvGroup, ID: threadID, Name: repo},
		Text:         c.Body,
		At:           at.UTC(),
		Channel: domain.ChannelRef{
			Source: string(Name),
			Thread: threadID,
			Address: map[string]string{
				"repository": repo,
				"number":     itoa(number),
				"url":        url,
				"kind":       kind,
			},
		},
		DefaultWorkspace: workspace,
	}
	return tr, nil
}

// IsBot 判断一个 GitHub login 是不是机器账号。主判据是 [bot] 后缀（GitHub
// App 机器人如 dependabot[bot] 都这样），这是任务要求的口径，也最容易在测试
// 里构造。API 的 type 字段由 isHumanUser 兜底。
func IsBot(login string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(login)), "[bot]")
}

// isHumanUser 判断一条评论是否人发的。宁可少认：未知 type 只有在确实是普通
// 用户时才当人；Organization 之类的账号不当成能发起工作的人。
func isHumanUser(u apiUser) bool {
	if IsBot(u.Login) {
		return false
	}
	return u.Type == "" || u.Type == "User"
}

// MentionsAgent 判断正文是否明确 @ 了 agent 的 handle。
//
// 直接写正则会在 Go 里踩坑：RE2 不支持 (?!...)，所以手写边界判定，语义与原型
// 的 `(?:^|[^a-zA-Z0-9_@])@handle(?![a-zA-Z0-9_-])` 一致：
//   - 前面不能是字母/数字/下划线/@（避免 a@handle.com、x@handle）；
//   - 后面不能是字母/数字/-/_（@handle-agent 不是叫这个 agent）。
//
// 大小写不敏感，因为 GitHub 的 handle 大小写不敏感。注意 @所有人、@everyone
// 不会命中，除非 agent 的 handle 恰好叫 everyone——这正是 §4.9 要的。
func MentionsAgent(text, handle string) bool {
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if handle == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		if text[i] != '@' {
			continue
		}
		end := i + 1 + len(handle)
		if end > len(text) || !strings.EqualFold(text[i+1:end], handle) {
			continue
		}
		if i > 0 && isMentionPrefix(text[i-1]) {
			continue
		}
		if end < len(text) && isMentionSuffix(text[end]) {
			continue
		}
		return true
	}
	return false
}

// isMentionPrefix 报告前面的字符是否让这个 @ 不是"开始一个 mention"。
func isMentionPrefix(b byte) bool {
	return b == '@' || b == '_' || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9')
}

// isMentionSuffix 报告后面的字符是否让 handle 变成更长 handle 的前缀。
func isMentionSuffix(b byte) bool {
	return b == '_' || b == '-' || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9')
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// itoa 只给地址 map 用：Address 是字符串空间，数字要显式转。
func itoa(n int) string { return fmt.Sprint(n) }

// apiIssue 是 Issue/PR 列表项（/issues 端点）里我们用到的字段。PR 也出现在
// /issues 里，靠 pull_request 字段区分；merged_at 只有 PR 且已合并时才有值。
type apiIssue struct {
	ID          int64      `json:"id"`
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	State       string     `json:"state"`
	HTMLURL     string     `json:"html_url"`
	User        *apiUser   `json:"user"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	PullRequest *struct {
		MergedAt *time.Time `json:"merged_at"`
	} `json:"pull_request"`
}

// issueTrigger 把 Issue/PR 本体翻译成 Trigger（ReceiveAll 模式）。
//
// PR 的生命周期（open → closed/merged）不需要单独的事件类型：GitHub 的
// updated_at 在状态变化时会变，而 Trigger ID 的版本段就是 updated_at，所以
// "同一个 PR 状态变了"天然是一条新 Trigger，内容派生的 ID 又保证重读不重复。
func (s *Source) issueTrigger(data []byte, pull bool) (domain.Trigger, error) {
	var it apiIssue
	if err := json.Unmarshal(data, &it); err != nil {
		return domain.Trigger{}, fmt.Errorf("github: unreadable issue: %w", err)
	}
	if it.Number <= 0 {
		return domain.Trigger{}, errors.New("github: issue has no number")
	}
	if it.User == nil || strings.TrimSpace(it.User.Login) == "" {
		return domain.Trigger{}, errors.New("github: issue has no author")
	}
	at := it.CreatedAt
	version := it.CreatedAt.UTC().Format(time.RFC3339Nano)
	if it.UpdatedAt != nil && !it.UpdatedAt.IsZero() {
		at = *it.UpdatedAt
		version = it.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	if at.IsZero() {
		return domain.Trigger{}, fmt.Errorf("github: issue %d has no timestamp", it.Number)
	}
	kind := RawIssue
	kindWord := "issue"
	kindRow := RowIssue
	if pull {
		kind = RawPull
		kindWord = "pull"
		kindRow = RowPull
	}
	scope, workspace := s.triggerScope()
	threadID := ThreadID(s.cfg.Repository, it.Number)
	url := it.HTMLURL
	if url == "" {
		url = PageURL(s.cfg.Repository, it.Number, pull)
	}
	status := issueStatus(it, pull)
	text := issueText(it, pull, status)
	return domain.Trigger{
		ID:      domain.Key(scope, "github-"+kind, s.cfg.Repository, it.Number, it.ID, version),
		Scope:   scope,
		Source:  string(Name),
		Threads: []domain.ThreadRef{{Source: string(Name), ID: threadID}},
		Actor:   domain.Actor{ID: it.User.Login, Bot: IsBot(it.User.Login)},
		Human:   isHumanUser(*it.User) && !strings.EqualFold(it.User.Login, s.cfg.Login),
		Self:    s.isSelf(it.User.Login),
		// Issue/PR 正文里的 @handle 与评论同义：明确 @ 到 agent 才算 addressed。
		Addressed: domain.Addressed{Mention: MentionsAgent(it.Body+"\n"+it.Title, s.mention())},
		Row:       kindRow,
		// Issue/PR 流只在 ReceiveAll 下才读，所以"接收全部"是事实。
		ScopeReceivesAll: s.cfg.ReceiveAll,
		Conversation:     domain.Conversation{Kind: domain.ConvGroup, ID: threadID, Name: s.cfg.Repository},
		Text:             text,
		At:               at.UTC(),
		Channel: domain.ChannelRef{
			Source: string(Name),
			Thread: threadID,
			Address: map[string]string{
				"repository": s.cfg.Repository,
				"number":     itoa(it.Number),
				"url":        url,
				"kind":       kind,
				"state":      status,
				"word":       kindWord,
			},
		},
		DefaultWorkspace: workspace,
	}, nil
}

// issueStatus 把 Issue/PR 的状态压成一句人能读的事实（"pull request merged"
// 等），写进正文与 Address，面板/模型不用再猜。
func issueStatus(it apiIssue, pull bool) string {
	if !pull {
		if it.State != "" {
			return "issue " + it.State
		}
		return ""
	}
	if it.PullRequest != nil && it.PullRequest.MergedAt != nil {
		return "pull request merged"
	}
	if it.State != "" {
		return "pull request " + it.State
	}
	return ""
}

// issueText 是 Issue/PR 交给模型的可读正文：标题 + 正文 + 状态行。
func issueText(it apiIssue, pull bool, status string) string {
	word := "issue"
	if pull {
		word = "pull"
	}
	var b strings.Builder
	if title := strings.TrimSpace(it.Title); title != "" {
		b.WriteString(title)
	}
	if body := strings.TrimSpace(it.Body); body != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(body)
	}
	if b.Len() == 0 {
		b.WriteString(word + " #" + itoa(it.Number))
	}
	if status != "" {
		b.WriteString("\n\n[" + status + "]")
	}
	return b.String()
}

// CIThreadID 是默认分支 CI 的 Thread id。它不是 Issue/PR 号，所以不复用
// ThreadID 的 `repo#number` 形状；订阅列表里带它的 Thread 会被 SplitThreadID
// 判为不可解析并跳过（CI 不按 Thread 订阅，而是 Source 级开关）。
func CIThreadID(repo, branch string) string { return repo + "#ci:" + branch }

// ciSnapshot 是 pollBranchCI 交给翻译层的快照形状（不含请求细节，便于测试
// 直接构造）。Failures/Running 是检查名，已排序。
type ciSnapshot struct {
	Branch   string   `json:"branch"`
	SHA      string   `json:"sha"`
	URL      string   `json:"url"`
	Summary  string   `json:"summary"`
	Total    int      `json:"total"`
	Failures []string `json:"failures"`
	Running  []string `json:"running"`
	Revision int      `json:"revision"`
}

// ciTrigger 把一次"CI 失败集合变化"翻译成 Trigger。
//
// 它不是人说的，所以 Human=false、Addressed 为空；它仍是一条 Trigger，
// 因为原型把它当作"能唤醒在跑 Ticket 的通知"（003/006）。Thread 是分支级
// 的 CI Thread，方便把同一条分支的多次 CI 变化串起来。
func (s *Source) ciTrigger(data []byte) (domain.Trigger, error) {
	var snap ciSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return domain.Trigger{}, fmt.Errorf("github: unreadable ci snapshot: %w", err)
	}
	if snap.Branch == "" || snap.SHA == "" {
		return domain.Trigger{}, errors.New("github: ci snapshot has no branch or head")
	}
	if snap.Revision <= 0 {
		return domain.Trigger{}, errors.New("github: ci snapshot has no revision")
	}
	scope, workspace := s.triggerScope()
	threadID := CIThreadID(s.cfg.Repository, snap.Branch)
	status := "ci " + snap.Branch
	if len(snap.Failures) > 0 {
		status += " failed"
	}
	return domain.Trigger{
		ID:      domain.Key(scope, "github-ci", s.cfg.Repository, "branch:"+snap.Branch, strconv.Itoa(snap.Revision)),
		Scope:   scope,
		Source:  string(Name),
		Threads: []domain.ThreadRef{{Source: string(Name), ID: threadID}},
		// CI 没有作者；Actor 留空而不是借用 agent 账号，避免被当成"agent 说的"。
		Actor: domain.Actor{},
		Human: false,
		Row:   RowCI,
		// CI 是广播事件而不是"对 agent 说话"：它靠 scope 接收全部 + admission
		// 行（unlistedUnaddressed cell）决定是否成为候选（002:16,79）。
		ScopeReceivesAll: true,
		Conversation:     domain.Conversation{Kind: domain.ConvGroup, ID: threadID, Name: s.cfg.Repository},
		Text:             snap.Summary,
		At:               s.now().UTC(),
		Channel: domain.ChannelRef{
			Source: string(Name),
			Thread: threadID,
			Address: map[string]string{
				"repository": s.cfg.Repository,
				"kind":       RawCI,
				"branch":     snap.Branch,
				"sha":        snap.SHA,
				"url":        snap.URL,
				"state":      status,
				"revision":   strconv.Itoa(snap.Revision),
			},
		},
		DefaultWorkspace: workspace,
	}, nil
}
