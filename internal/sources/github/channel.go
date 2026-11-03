package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/sources"
)

// Channel 返回回写适配器。GitHub 的回复就是一条 Issue/PR 评论。
func (s *Source) Channel() sources.Channel {
	return &channel{cmd: s.cmd, cfg: s.cfg}
}

// channel 是 sources.Channel 的 GitHub 实现。
type channel struct {
	cmd Command
	cfg Config
}

// markerOf 生成幂等标记：写进评论正文的隐藏 HTML 注释。
//
// 用 outbox key 的哈希而不是 key 原文，有两个原因：key 来自外部（Outbox
// 可能用任意业务字符串），不能让它把 HTML 注释提前闭合；而固定长度的十六
// 进制也能保证标记本身不会干扰正文渲染。
func markerOf(key string) string {
	return "<!-- zeokeel:" + string(domain.Key(domain.Global, "reply-marker", key)) + " -->"
}

// Send 幂等发表一条评论。流程刻意做成"先查后发"：
//
//   - 发送前先翻这个 Thread 的评论，找我们自己写过、正文里带同一个 marker 的
//     那条。找到就说明上一次其实成功了、只是回执没记下来——直接返回成功，
//     不再发第二条。
//   - 查不到才 POST。
//
// 这是在"至少一次"的投递语义上补出"至多一条"的效果（§7.2 出站幂等）。
func (c *channel) Send(ctx context.Context, msg sources.Outbound) (sources.Receipt, error) {
	repo, number, err := c.target(msg)
	if err != nil {
		return sources.Receipt{}, err
	}
	if strings.TrimSpace(msg.Key) == "" {
		// 没有幂等键就不能保证不重复；宁可报错让 Outbox 补，也不盲发。
		return sources.Receipt{}, errors.New("github: outbound message has no idempotency key")
	}
	marker := markerOf(msg.Key)

	existing, err := c.find(ctx, repo, number, marker)
	if err != nil {
		return sources.Receipt{}, err
	}
	if existing != "" {
		return sources.Receipt{MessageID: existing}, nil
	}

	body := strings.TrimRight(msg.Text, "\n") + "\n\n" + marker
	out, err := c.cmd.Run(ctx, "api", "-X", "POST",
		fmt.Sprintf("repos/%s/issues/%d/comments", repo, number),
		"-f", "body="+body)
	if err != nil {
		if isGone(err) {
			// Issue 被删/被锁：永久不可达，Outbox 应丢弃而不是无限重试。
			return sources.Receipt{Gone: true}, nil
		}
		return sources.Receipt{}, fmt.Errorf("github: post comment: %w", err)
	}
	return sources.Receipt{MessageID: createdID(out)}, nil
}

// target 从 Outbound 里解出仓库与 Issue/PR 号：优先用 Trigger 带过来的
// Address，其次解析 Thread id。两者都缺就是配置/调用错误。
func (c *channel) target(msg sources.Outbound) (string, int, error) {
	repo := msg.Address["repository"]
	numberStr := msg.Address["number"]
	if repo == "" || numberStr == "" {
		threadRepo, threadNumber, ok := SplitThreadID(msg.Thread)
		if !ok {
			return "", 0, errors.New("github: outbound message has no reply target")
		}
		if repo == "" {
			repo = threadRepo
		}
		if numberStr == "" {
			numberStr = strconv.Itoa(threadNumber)
		}
	}
	if !sameRepository(repo, c.cfg.Repository) {
		return "", 0, fmt.Errorf("github: reply target repository %q is not %q", repo, c.cfg.Repository)
	}
	number, err := strconv.Atoi(numberStr)
	if err != nil || number <= 0 {
		return "", 0, fmt.Errorf("github: reply target %q is not an issue or pull request number", numberStr)
	}
	return repo, number, nil
}

// find 在 Thread 的评论里找我们自己发的、带 marker 的那条。返回它的 id。
//
// 读失败时返回 error 而不是"当作没找到"：查不清是否已发就贸然再发，会造出
// 重复评论；宁可不发，让 Outbox 重试。
func (c *channel) find(ctx context.Context, repo string, number int, marker string) (string, error) {
	// 必须翻完所有页：只看第一页会漏掉后面的 marker，重发就变成重复评论
	// （N-14）。页数有硬上限，超过就失败关闭（宁可让 Outbox 重试，也不重复评论）。
	for page := 1; ; page++ {
		if page > maxMarkerPages {
			return "", fmt.Errorf("github: cannot verify idempotency: thread has more than %d comment pages", maxMarkerPages)
		}
		path := fmt.Sprintf("repos/%s/issues/%d/comments?per_page=%d&page=%d", repo, number, listPageSize, page)
		out, err := c.cmd.Run(ctx, "api", path)
		if err != nil {
			return "", fmt.Errorf("github: read thread before replying: %w", err)
		}
		var comments []struct {
			ID   int64    `json:"id"`
			Body string   `json:"body"`
			User *apiUser `json:"user"`
		}
		if err := json.Unmarshal(out, &comments); err != nil {
			return "", fmt.Errorf("github: unreadable thread comments: %w", err)
		}
		for _, comment := range comments {
			// 只认我们自己账号写的标记：别人正文里恰好出现同样字符串不算。
			if comment.User == nil || !strings.EqualFold(comment.User.Login, c.cfg.Login) {
				continue
			}
			if strings.Contains(comment.Body, marker) {
				return strconv.FormatInt(comment.ID, 10), nil
			}
		}
		if len(comments) < listPageSize {
			return "", nil
		}
	}
}

// createdID 从 POST 的响应里取新评论 id。取不到不算失败：消息已经发出去了，
// 回执 id 只是对账用的锦上添花。
func createdID(out []byte) string {
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil || created.ID == 0 {
		return ""
	}
	return strconv.FormatInt(created.ID, 10)
}

// isGone 判断错误是否意味着目标永久不可达。
//
// 410 是 GitHub 明确说"删了"；403 + locked 是被锁讨论。404 故意不算 gone：
// 私有仓库权限暂时丢失也会返回 404，立刻放弃会丢消息，重试才是对的。
func isGone(err error) bool {
	cliErr, ok := AsCLIError(err)
	if !ok {
		return false
	}
	if cliErr.Code == 410 {
		return true
	}
	return cliErr.Code == 403 && strings.Contains(strings.ToLower(cliErr.Stderr), "locked")
}
