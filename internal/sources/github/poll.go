package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/sources"
)

const (
	// cursorSlack 是游标回退量。把下一轮的 since 往回拨 5 秒，晚到的事件
	// 不会漏；重叠的那几条靠内容派生 ID 去重（原型 poll.cursorAt 同理）。
	cursorSlack = 5 * time.Second

	// listPageSize 是每一次分页请求的条数，也是"还有下一页"的判据：返回
	// 少于一页就说明到尾了。原型用 per_page=100 并在 gh 里翻页，这里把翻页
	// 放在 Go 里，测试可以确定性地喂多页。
	listPageSize = 100

	// maxListPages 是单次轮询一个流的分页上限。超过它说明 since 窗口内的
	// 数据量异常（例如长时间休眠后补账），此时**报错并保持游标**，让下一轮
	// 重试，而不是静默丢掉剩下的页（N-14 的"50/100 上限静默丢事件"）。
	maxListPages = 20

	// maxMarkerPages 是"发送前找 marker"的翻页上限。找不到就必须失败关闭：
	// 查不清是否已发就贸然再发会制造重复评论（N-14）。
	maxMarkerPages = 50
)

// Raw 事件的 Kind。评论是默认流；ReceiveAll 打开后还会产出 Issue/PR 本体
// 与 PR 行内 review 评论。
const (
	RawComment       = "comment"
	RawReviewComment = "review-comment"
	RawIssue         = "issue"
	RawPull          = "pull"
	RawCI            = "ci"
)

// listPage 是一个分页 GET 的结果。NotModified 只可能出现在第一页：条件请求
// 只对"同一个 since + 同一个 ETag"的重复请求有意义。
type listPage struct {
	Items       []json.RawMessage
	ETag        string
	NotModified bool
}

// HeaderCommand 是 Command 的可选能力：一次 GET 同时给出响应头与状态码。
//
// 为什么需要它：GitHub 的 rate limit 里，条件请求返回 304 不计费。gh 的
// `api -i` 会把状态行 + 响应头 + body 打到 stdout（HTTP 错误/304 时 gh 仍以
// 非零退出，但 stdout 还在），所以解析 stdout 比解析 stderr 文本更可靠。
// 没有实现本接口的 Command（测试里的简化假实现）会退化为普通 GET：功能不变，
// 只是没有 ETag/304 优化。
type HeaderCommand interface {
	RunWithHeaders(ctx context.Context, args ...string) (body []byte, status int, header http.Header, err error)
}

// cursorState 是 GitHub Source 的读取位置。
//
// Since 是下一轮 since 参数；ETag 只在 Since 没变时复用（条件请求的语义：
// "自从我刚才问过的那个时间点起，这个列表有没有变"）。把两个流的 ETag 分开
// 存，因为通知与评论是两个独立的列表。
type cursorState struct {
	Repo        string
	Since       time.Time
	NotifETag   string
	CommentETag string
	// 默认分支 CI 的基线：第一次观察只记基线（不发 Trigger），失败集合变化
	// 才发一条（原型 pollBranchCi）。Revision 让每次变化有唯一 Trigger ID。
	CIBranch   string
	CIFailures []string
	CIRevision int
}

// encodeCursorState 把游标写成 `repo|<RFC3339Nano>|<notifETag>|<commentETag>`，
// ETag 用 base64 是因为 ETag 里可能有 `"`/`/`/`W/`，放进 `|` 分隔的字符串会
// 破坏解析。缺省的尾部字段允许空串，便于人读日志。
func encodeCursorState(c cursorState) sources.Cursor {
	enc := base64.RawURLEncoding.EncodeToString
	return sources.Cursor(c.Repo + "|" + c.Since.UTC().Format(time.RFC3339Nano) + "|" +
		enc([]byte(c.NotifETag)) + "|" + enc([]byte(c.CommentETag)) + "|" +
		enc([]byte(c.CIBranch)) + "|" + enc([]byte(strings.Join(c.CIFailures, "\x1f"))) + "|" +
		fmt.Sprint(c.CIRevision))
}

// decodeCursor 解出游标。旧格式 `repo|<time>`（没有 ETag 字段）仍然可读：
// 升级后的第一次轮询只是没有条件请求，不影响正确性。
func decodeCursor(raw sources.Cursor) (cursorState, bool) {
	parts := strings.Split(string(raw), "|")
	if len(parts) < 2 {
		return cursorState{}, false
	}
	when, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return cursorState{}, false
	}
	c := cursorState{Repo: parts[0], Since: when.UTC()}
	if len(parts) > 2 {
		if etag, err := base64.RawURLEncoding.DecodeString(parts[2]); err == nil {
			c.NotifETag = string(etag)
		}
	}
	if len(parts) > 3 {
		if etag, err := base64.RawURLEncoding.DecodeString(parts[3]); err == nil {
			c.CommentETag = string(etag)
		}
	}
	if len(parts) > 4 {
		if branch, err := base64.RawURLEncoding.DecodeString(parts[4]); err == nil {
			c.CIBranch = string(branch)
		}
	}
	if len(parts) > 5 {
		if failures, err := base64.RawURLEncoding.DecodeString(parts[5]); err == nil && len(failures) > 0 {
			c.CIFailures = strings.Split(string(failures), "\x1f")
		}
	}
	if len(parts) > 6 {
		if revision, err := strconv.Atoi(parts[6]); err == nil {
			c.CIRevision = revision
		}
	}
	return c, true
}

// cursorOf 解出本轮读取起点。空/坏/换了仓库的游标退回"现在"：新装实例绝不
// 从头重放历史，坏游标也不该让轮询永久卡死（宁可漏一小段）。
func (s *Source) cursorOf(c sources.Cursor, started time.Time) cursorState {
	if c == "" {
		return cursorState{Repo: s.cfg.Repository, Since: started}
	}
	decoded, ok := decodeCursor(c)
	if !ok || !sameRepository(decoded.Repo, s.cfg.Repository) {
		return cursorState{Repo: s.cfg.Repository, Since: started}
	}
	return decoded
}

// encodeCursor 是旧签名（仓库 + 时间）的构造器，保留给测试与日志工具用。
func encodeCursor(repo string, at time.Time) sources.Cursor {
	return encodeCursorState(cursorState{Repo: repo, Since: at.UTC()})
}

// sinceOf 返回游标里的读取起点（供测试与面板显示用）。
func (s *Source) sinceOf(c sources.Cursor, started time.Time) time.Time {
	return s.cursorOf(c, started).Since
}

// Poll 实现 sources.Poller：读通知 → 读相关 Issue/PR 的评论（ReceiveAll 时还
// 读仓库级 Issue/PR 流与 review 评论）→ 逐条翻译、过滤、receive，全部成功后
// 才推进游标。
//
// 游标纪律（接口契约的核心）：res.Next 一开始等于 req.Cursor，只有整轮没有
// 任何错误时才在最后改写它。任何一步失败就直接返回，调用方看到 err != nil
// 就不会持久化 Next，下一轮从头重读这一批——重复的 Trigger 会被内容 ID 挡掉。
func (s *Source) Poll(ctx context.Context, req sources.PollRequest, receive sources.Receiver) (sources.PollResult, error) {
	res := sources.PollResult{Next: req.Cursor}
	if receive == nil {
		return res, errors.New("github: nil receiver")
	}
	started := s.now().UTC()
	cur := s.cursorOf(req.Cursor, started)

	// 1. 通知给出"本轮值得读的 Thread"；订阅的 Thread 也并进来（推 + 拉两条
	//    来源，和原型一致）。通知本身不会变成 Trigger。
	numbers, notifETag, _, unreadable, err := s.notifiedNumbers(ctx, cur)
	if err != nil {
		return res, err
	}
	res.Unreadable += unreadable
	for _, ref := range req.Threads {
		if ref.Source != string(Name) {
			continue
		}
		repo, number, ok := SplitThreadID(ref.ID)
		if !ok || !sameRepository(repo, s.cfg.Repository) {
			continue
		}
		numbers[number] = true
	}

	// 2. 评论流。ReceiveAll 打开时不再依赖"被通知/已订阅"，整个仓库的评论都读。
	observeComments := s.cfg.ObserveComments || s.cfg.ReceiveAll
	var comments listPage
	commentETag := cur.CommentETag
	if observeComments || len(numbers) > 0 {
		comments, err = s.list(ctx, "repos/"+s.cfg.Repository+"/issues/comments?sort=updated&direction=asc", cur.Since, cur.CommentETag)
		if err != nil {
			return res, err
		}
		if err := s.pollComments(ctx, numbers, comments.Items, observeComments, receive, &res); err != nil {
			return res, err
		}
		if !comments.NotModified {
			commentETag = comments.ETag
		}
	}

	// 3. ReceiveAll：仓库级 Issue/PR 流与 PR 行内 review 评论流。
	if s.cfg.ReceiveAll {
		if err := s.pollIssueStream(ctx, receive, &res); err != nil {
			return res, err
		}
		if err := s.pollReviewComments(ctx, receive, &res); err != nil {
			return res, err
		}
	}

	// 4. 默认分支 CI（ObserveCI）：失败集合变化才发一条；第一次只建基线。
	nextCI := cursorState{CIBranch: cur.CIBranch, CIFailures: cur.CIFailures, CIRevision: cur.CIRevision}
	if s.cfg.ObserveCI {
		nextCI, err = s.pollBranchCI(ctx, cur, receive, &res)
		if err != nil {
			return res, err
		}
	}

	// 4. 全部 receive 都成功了，游标才可以动。Since 只在真的有更新的评论时
	//    前进（原型 readStream 的 moved > since 语义）：没前进时 ETag 才有
	//    意义，下一轮条件请求可以拿 304 省 rate limit。
	next := cur
	newest := newestCommentTime(comments.Items)
	if !newest.IsZero() {
		moved := newest.Add(-cursorSlack)
		if moved.After(next.Since) {
			next.Since = moved
		}
	}
	next.Repo = s.cfg.Repository
	next.CIBranch, next.CIFailures, next.CIRevision = nextCI.CIBranch, nextCI.CIFailures, nextCI.CIRevision
	next.NotifETag = carriedETag(cur.Since, next.Since, notifETag)
	next.CommentETag = carriedETag(cur.Since, next.Since, commentETag)
	res.Next = encodeCursorState(next)
	return res, nil
}

// carriedETag 决定 ETag 是否能带到下一轮：只有当 since 没前进（下一轮会重复
// 同一个查询）并且本轮确实拿到了列表响应时才保留。
func carriedETag(prevSince, nextSince time.Time, etag string) string {
	if !prevSince.Equal(nextSince) || etag == "" {
		return ""
	}
	return etag
}

// list 读一个仓库级列表的全部页。第一页带条件请求（ETag），后续页不带；
// 304 只对第一页有意义，返回 NotModified 而不是错误。
func (s *Source) list(ctx context.Context, path string, since time.Time, etag string) (listPage, error) {
	base := path + "&per_page=" + fmt.Sprint(listPageSize) + "&since=" + url.QueryEscape(since.Format(time.RFC3339))
	var page listPage
	for n := 1; ; n++ {
		if n > maxListPages {
			return listPage{}, fmt.Errorf("github: %s pagination exceeded %d pages", path, maxListPages)
		}
		itemPath := fmt.Sprintf("%s&page=%d", base, n)
		pageETag := ""
		if n == 1 {
			pageETag = etag
		}
		body, status, header, err := s.fetch(ctx, itemPath, pageETag)
		if err != nil {
			return listPage{}, err
		}
		if n == 1 && status == http.StatusNotModified {
			// 列表没变：不读 body，保留原 ETag，since 也不动。
			return listPage{ETag: etag, NotModified: true}, nil
		}
		items, err := decodeRawList(body)
		if err != nil {
			return listPage{}, fmt.Errorf("github: %s page %d: %w", path, n, err)
		}
		page.Items = append(page.Items, items...)
		if header != nil {
			page.ETag = header.Get("ETag")
		}
		if len(items) < listPageSize {
			return page, nil
		}
	}
}

// fetch 执行一次 API GET。有 HeaderCommand 时用 `api -i`（拿状态码/响应头，
// 支持 ETag 与 304）；否则退化为普通调用（无 ETag 优化，但功能完整）。
func (s *Source) fetch(ctx context.Context, path, etag string) ([]byte, int, http.Header, error) {
	hc, ok := s.cmd.(HeaderCommand)
	if !ok {
		args := []string{"api", path}
		if etag != "" {
			args = []string{"api", "-H", "If-None-Match: " + etag, path}
		}
		body, err := s.cmd.Run(ctx, args...)
		return body, 0, nil, err
	}
	args := []string{"api", "-i"}
	if etag != "" {
		args = append(args, "-H", "If-None-Match: "+etag)
	}
	args = append(args, path)
	return hc.RunWithHeaders(ctx, args...)
}

// decodeRawList 解一个 JSON 数组；空 body（某些 304 场景）按空列表处理。
func decodeRawList(data []byte) ([]json.RawMessage, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// newestCommentTime 返回一批评论里最新的 updated_at（缺省 created_at）。
// 用"最新一条"而不是"本轮开始时刻"来推进游标，是原型 readStream 的做法：
// 没有新数据时游标不动，下一轮才能用 ETag 换 304。
func newestCommentTime(items []json.RawMessage) time.Time {
	var newest time.Time
	for _, raw := range items {
		var stamp struct {
			UpdatedAt *time.Time `json:"updated_at"`
			CreatedAt time.Time  `json:"created_at"`
		}
		if err := json.Unmarshal(raw, &stamp); err != nil {
			continue
		}
		at := stamp.CreatedAt
		if stamp.UpdatedAt != nil && !stamp.UpdatedAt.IsZero() {
			at = *stamp.UpdatedAt
		}
		if at.After(newest) {
			newest = at
		}
	}
	return newest
}

// apiNotification 是 GitHub 通知里我们需要的部分。通知只用来知道"哪些
// Thread 有新动静"，本身不会变成 Trigger。
type apiNotification struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Subject struct {
		URL string `json:"url"`
	} `json:"subject"`
}

// notifiedNumbers 读 GitHub 通知（分页 + 条件请求），返回仓库内被通知的
// Issue/PR 编号。别的仓库、review discussion 之类解析不出 Issue 号的通知
// 直接忽略：它们不在本 Source 的地盘里。
func (s *Source) notifiedNumbers(ctx context.Context, cur cursorState) (map[int]bool, string, bool, int, error) {
	page, err := s.list(ctx, "notifications?all=true", cur.Since, cur.NotifETag)
	if err != nil {
		return nil, "", false, 0, fmt.Errorf("github: read notifications: %w", err)
	}
	if page.NotModified {
		return map[int]bool{}, cur.NotifETag, true, 0, nil
	}
	items, unreadable, err := decodeItems[apiNotification](page.Items)
	if err != nil {
		return nil, "", false, 0, fmt.Errorf("github: notifications: %w", err)
	}
	numbers := make(map[int]bool, len(items))
	for _, item := range items {
		if !sameRepository(item.Repository.FullName, s.cfg.Repository) {
			continue
		}
		repo, number, _, ok := ParseIssueRef(item.Subject.URL)
		if !ok || !sameRepository(repo, s.cfg.Repository) {
			continue
		}
		numbers[number] = true
	}
	return numbers, page.ETag, false, unreadable, nil
}

// pollComments 读仓库的 Issue/PR 对话评论，逐条翻译与过滤，把该收的交给
// receive。
//
// 坏 item 的处理是逐条容错：一条坏 JSON 只让自己消失并计入 Unreadable，
// 同批其他能干活的人不受影响。
func (s *Source) pollComments(ctx context.Context, numbers map[int]bool, items []json.RawMessage, observe bool, receive sources.Receiver, res *sources.PollResult) error {
	for _, raw := range items {
		tr, err := s.Translate(sources.Raw{Kind: RawComment, Data: raw})
		if err != nil {
			res.Unreadable++
			continue
		}
		// Agent 自己的评论永远不是请求。GitHub 会把它们也列出来，收下只会
		// 自己叫自己（原型的 intakeComments 同样跳过 agent 的 login）。
		if strings.EqualFold(tr.Actor.ID, s.cfg.Login) {
			res.Filtered++
			continue
		}
		// 空评论没有可处理的内容（例如只有一张图）；不作为请求交给 Gate。
		if strings.TrimSpace(tr.Text) == "" {
			res.Filtered++
			continue
		}
		_, number, ok := firstThread(&tr)
		if !ok {
			res.Unreadable++
			continue
		}
		// 没开"接收全部"时只读被通知或已订阅的 Thread；仓库里其他讨论轮不到
		// 本 Source 管，计数但不 receive。
		if !observe && !numbers[number] {
			res.Filtered++
			continue
		}
		if scope := s.Scope(tr); !scope.Listened {
			res.Filtered++
			continue
		}
		if err := receive(tr); err != nil {
			// 一条 receive 失败就不能推进游标；调用方重试这一批，前面的
			// Trigger 因为有内容派生 ID 不会重复建单。
			return fmt.Errorf("github: receive trigger %s: %w", tr.ID, err)
		}
		res.Received++
	}
	return nil
}

// pollIssueStream 读仓库级的 Issue/PR 流（ReceiveAll）。GitHub 的 /issues
// 端点同时返回 PR，`pull_request` 字段区分两者；`state=all` 才能看到合并/关闭
// 这类生命周期变化（原型 STREAMS.issues 同样是 state=all）。
func (s *Source) pollIssueStream(ctx context.Context, receive sources.Receiver, res *sources.PollResult) error {
	page, err := s.list(ctx, "repos/"+s.cfg.Repository+"/issues?state=all&sort=updated&direction=asc", s.sinceForStream(), "")
	if err != nil {
		return fmt.Errorf("github: read issues: %w", err)
	}
	return s.receiveIssueItems(page.Items, receive, res)
}

// sinceForStream 给独立记账的流一个起点：ReceiveAll 的流没有单独游标，用
// "本轮开始时刻 - 30 秒"作为窗口；重复项由内容派生 ID 吸收。30 秒覆盖一次
// 轮询间隔的漂移，代价只是每次多看一小段（无法用 ETag 的条件请求仍然安全）。
func (s *Source) sinceForStream() time.Time { return s.now().UTC().Add(-30 * time.Second) }

// receiveIssueItems 把 Issue/PR 列表项翻译、过滤并交给 receive。
func (s *Source) receiveIssueItems(items []json.RawMessage, receive sources.Receiver, res *sources.PollResult) error {
	for _, raw := range items {
		var probe struct {
			PullRequest *json.RawMessage `json:"pull_request"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			res.Unreadable++
			continue
		}
		kind := RawIssue
		if probe.PullRequest != nil {
			kind = RawPull
		}
		tr, err := s.Translate(sources.Raw{Kind: kind, Data: raw})
		if err != nil {
			res.Unreadable++
			continue
		}
		if strings.EqualFold(tr.Actor.ID, s.cfg.Login) {
			res.Filtered++
			continue
		}
		if strings.TrimSpace(tr.Text) == "" {
			res.Filtered++
			continue
		}
		if scope := s.Scope(tr); !scope.Listened {
			res.Filtered++
			continue
		}
		if err := receive(tr); err != nil {
			return fmt.Errorf("github: receive trigger %s: %w", tr.ID, err)
		}
		res.Received++
	}
	return nil
}

// pollReviewComments 读 PR 行内 review 评论（ReceiveAll）。它们不属于
// "对话评论"，但同样是人对 agent 说的话（原型 review-comments 流）。
func (s *Source) pollReviewComments(ctx context.Context, receive sources.Receiver, res *sources.PollResult) error {
	page, err := s.list(ctx, "repos/"+s.cfg.Repository+"/pulls/comments?sort=updated&direction=asc", s.sinceForStream(), "")
	if err != nil {
		return fmt.Errorf("github: read review comments: %w", err)
	}
	for _, raw := range page.Items {
		tr, err := s.Translate(sources.Raw{Kind: RawReviewComment, Data: raw})
		if err != nil {
			res.Unreadable++
			continue
		}
		if strings.EqualFold(tr.Actor.ID, s.cfg.Login) {
			res.Filtered++
			continue
		}
		if strings.TrimSpace(tr.Text) == "" {
			res.Filtered++
			continue
		}
		if scope := s.Scope(tr); !scope.Listened {
			res.Filtered++
			continue
		}
		if err := receive(tr); err != nil {
			return fmt.Errorf("github: receive trigger %s: %w", tr.ID, err)
		}
		res.Received++
	}
	return nil
}

// decodeItems 把 JSON 数组逐条解码成 T：外壳坏 → error（整批读不到）；某一条
// 坏 → 计入 unreadable，其余照常。这是"逐条容错"的公共实现。
func decodeItems[T any](data []json.RawMessage) (items []T, unreadable int, err error) {
	for _, raw := range data {
		var item T
		if err := json.Unmarshal(raw, &item); err != nil {
			unreadable++
			continue
		}
		items = append(items, item)
	}
	return items, unreadable, nil
}

// --- 默认分支 CI ---

// failingConclusion 是原型 FAILING 集合：这几种结论算失败；success/neutral/
// skipped/stale 不算。
var failingConclusion = map[string]bool{
	"failure": true, "timed_out": true, "cancelled": true,
	"action_required": true, "startup_failure": true, "error": true,
}

// checkRun 是一次 check-runs 响应里的单个检查。
type checkRun struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
}

// checkRunsPage 是 check-runs 列表响应。
type checkRunsPage struct {
	TotalCount int        `json:"total_count"`
	CheckRuns  []checkRun `json:"check_runs"`
}

// ciResult 是一次 CI 快照里我们需要的结论。
type ciResult struct {
	total    int
	failures []string // 排序、去重
	running  []string // 排序、去重
}

// pollBranchCI 读默认分支 HEAD 的 check-runs，失败集合变化时产出一条 Trigger。
//
// 语义（原型 pollBranchCi）：
//   - 第一次观察（或默认分支改名）只建立基线，不发 Trigger；
//   - 失败集合与基线不同才发一条，revision+1 让 Trigger ID 唯一；
//   - 检查还在跑时保留旧失败集合，避免"一半在跑"被报成恢复。
//
// 任何读取失败都让整轮失败（游标不动），下一轮重试。
func (s *Source) pollBranchCI(ctx context.Context, cur cursorState, receive sources.Receiver, res *sources.PollResult) (cursorState, error) {
	next := cursorState{CIBranch: cur.CIBranch, CIFailures: cur.CIFailures, CIRevision: cur.CIRevision}

	var repoInfo struct {
		DefaultBranch string `json:"default_branch"`
	}
	raw, err := s.cmd.Run(ctx, "api", "repos/"+s.cfg.Repository)
	if err != nil {
		return next, fmt.Errorf("github: read repository for ci: %w", err)
	}
	if err := json.Unmarshal(raw, &repoInfo); err != nil || repoInfo.DefaultBranch == "" {
		return next, fmt.Errorf("github: unreadable repository for ci: %w", err)
	}
	branch := repoInfo.DefaultBranch

	var head struct {
		SHA string `json:"sha"`
	}
	raw, err = s.cmd.Run(ctx, "api", "commits/"+url.PathEscape(branch))
	if err != nil {
		return next, fmt.Errorf("github: read %s head for ci: %w", branch, err)
	}
	if err := json.Unmarshal(raw, &head); err != nil || head.SHA == "" {
		return next, fmt.Errorf("github: unreadable %s head for ci: %w", branch, err)
	}

	result, err := s.readCheckRuns(ctx, head.SHA)
	if err != nil {
		return next, err
	}

	hasBaseline := cur.CIBranch == branch
	known := cur.CIFailures
	// 检查还在跑时不把"暂时没有失败"当成恢复：保留旧集合（原型同语义）。
	now := result.failures
	if len(now) == 0 && len(result.running) > 0 && hasBaseline {
		now = known
	}
	revision := cur.CIRevision
	if hasBaseline && !equalStrings(now, known) {
		revision++
		summary := ciSummary(branch, head.SHA, result)
		url := "https://github.com/" + s.cfg.Repository + "/commit/" + head.SHA
		payload, err := json.Marshal(ciSnapshot{
			Branch: branch, SHA: head.SHA, URL: url, Summary: summary,
			Total: result.total, Failures: result.failures, Running: result.running, Revision: revision,
		})
		if err != nil {
			return next, fmt.Errorf("github: encode ci snapshot: %w", err)
		}
		tr, err := s.Translate(sources.Raw{Kind: RawCI, Data: payload})
		if err != nil {
			return next, err
		}
		if scope := s.Scope(tr); !scope.Listened {
			res.Filtered++
		} else if err := receive(tr); err != nil {
			return next, fmt.Errorf("github: receive trigger %s: %w", tr.ID, err)
		} else {
			res.Received++
		}
	}
	next.CIBranch, next.CIFailures, next.CIRevision = branch, now, revision
	return next, nil
}

// readCheckRuns 读一个 commit 的全部 check-runs（分页）。
func (s *Source) readCheckRuns(ctx context.Context, sha string) (ciResult, error) {
	var result ciResult
	seen := map[string]bool{}
	for page := 1; ; page++ {
		if page > maxListPages {
			return result, fmt.Errorf("github: check-runs pagination exceeded %d pages", maxListPages)
		}
		path := fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=%d&page=%d",
			s.cfg.Repository, url.PathEscape(sha), listPageSize, page)
		raw, err := s.cmd.Run(ctx, "api", path)
		if err != nil {
			return result, fmt.Errorf("github: read check-runs: %w", err)
		}
		var pageData checkRunsPage
		if err := json.Unmarshal(raw, &pageData); err != nil {
			return result, fmt.Errorf("github: unreadable check-runs: %w", err)
		}
		if pageData.TotalCount > result.total {
			result.total = pageData.TotalCount
		}
		for _, run := range pageData.CheckRuns {
			if run.Name == "" {
				continue
			}
			if run.Conclusion != nil && failingConclusion[*run.Conclusion] {
				if !seen["f:"+run.Name] {
					seen["f:"+run.Name] = true
					result.failures = append(result.failures, run.Name)
				}
				continue
			}
			if run.Status != "" && run.Status != "completed" {
				if !seen["r:"+run.Name] {
					seen["r:"+run.Name] = true
					result.running = append(result.running, run.Name)
				}
			}
		}
		if len(pageData.CheckRuns) < listPageSize {
			break
		}
	}
	sort.Strings(result.failures)
	sort.Strings(result.running)
	return result, nil
}

// ciSummary 是一句人能读的 CI 结论（≤ 200 字符，IM/面板都放得下）。
func ciSummary(branch, sha string, result ciResult) string {
	where := branch + " at " + shortSHA(sha)
	names := func(list []string) string {
		const max = 5
		if len(list) > max {
			return strings.Join(list[:max], ", ") + fmt.Sprintf(" 等 %d 项", len(list))
		}
		return strings.Join(list, ", ")
	}
	switch {
	case len(result.failures) > 0:
		line := fmt.Sprintf("CI 失败 %d/%d on %s: %s", len(result.failures), result.total, where, names(result.failures))
		if len(result.running) > 0 {
			line += fmt.Sprintf("；%d 项仍在运行", len(result.running))
		}
		return line
	case len(result.running) > 0:
		return fmt.Sprintf("CI 进行中 %d/%d on %s: %s", len(result.running), result.total, where, names(result.running))
	default:
		return fmt.Sprintf("CI 通过 %d/%d on %s", result.total, result.total, where)
	}
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// equalStrings 比较两个有序字符串切片是否相同。
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
