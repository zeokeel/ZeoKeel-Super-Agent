package github

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ThreadID 是 GitHub Source 自己的 Thread id：仓库 + Issue/PR 号。
//
// 为什么把仓库写进 id：设计 §4.8 把 Thread 定义为"仓库 + issue/PR 号"。
// 一个 Source 实例只盯一个仓库，但 Thread key 会在日志、面板、Policy 里裸奔，
// 带上仓库名之后 `acme/webapp#12` 自己就能读，也不会和另一个仓库的 #12 撞。
func ThreadID(repo string, number int) string {
	return repo + "#" + strconv.Itoa(number)
}

// SplitThreadID 把 ThreadID 拆回仓库与编号。
func SplitThreadID(id string) (string, int, bool) {
	repo, num, ok := strings.Cut(id, "#")
	if !ok || repo == "" || num == "" {
		return "", 0, false
	}
	number, err := strconv.Atoi(num)
	if err != nil || number <= 0 {
		return "", 0, false
	}
	return repo, number, true
}

// issueRefPattern 同时认 API 地址与网页地址：
//
//	https://api.github.com/repos/acme/webapp/issues/12
//	https://api.github.com/repos/acme/webapp/pulls/12
//	https://github.com/acme/webapp/issues/12#issuecomment-1
//	https://github.com/acme/webapp/pull/12
//
// 末尾的 #fragment 与 query 都允许存在，因为我们只关心仓库和编号。
var issueRefPattern = regexp.MustCompile(`/([^/]+/[^/]+)/(issues|pulls?)/(\d+)(?:[/?#].*)?$`)

// ParseIssueRef 从 Issue/PR 的 URL 里取出仓库、编号与是否 PR。
// 通知、评论、Issue body 都用它，避免每个调用点各写一套正则。
func ParseIssueRef(url string) (repo string, number int, isPR bool, ok bool) {
	m := issueRefPattern.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", 0, false, false
	}
	number, err := strconv.Atoi(m[3])
	if err != nil || number <= 0 {
		return "", 0, false, false
	}
	return m[1], number, strings.HasPrefix(strings.ToLower(m[2]), "pull"), true
}

// PageURL 是 Issue/PR 的网页地址。html_url 缺失时用它补，回复与面板都需要
// 一个能点开的链接。
func PageURL(repo string, number int, isPR bool) string {
	kind := "issues"
	if isPR {
		kind = "pull"
	}
	return fmt.Sprintf("https://github.com/%s/%s/%d", repo, kind, number)
}

// sameRepository 比较仓库名。GitHub 的 owner/name 大小写不敏感，用 EqualFold
// 才不会因为 API 返回的大小写差异把本仓库的事件漏掉。
func sameRepository(a, b string) bool { return strings.EqualFold(a, b) }
