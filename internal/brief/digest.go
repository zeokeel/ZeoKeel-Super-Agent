package brief

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// DigestWindow is how far back the digest looks. Two weeks is long enough to show
// what the workspace has been used for and short enough that a quiet month does not
// look busy.
const DigestWindow = 14 * 24 * time.Hour

// DigestMaxRunes bounds the summary. The router reads one of these per candidate,
// so it competes with the brief for the same window (design §4.6).
const DigestMaxRunes = 200

// Digest summarises what a workspace has actually been doing: how many tickets it
// handled recently, what they were about, and what came out. It is derived from the
// durable tickets and artifacts on every call, never stored — a cached summary would
// go stale exactly when it matters (design §4.6: 简报是缓存不是真相).
//
// The output is deterministic for a given set of tickets, so prompt diffs stay
// readable and cache prefixes stay stable.
func Digest(w domain.Workspace, tickets []domain.Ticket, now time.Time, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = DigestMaxRunes
	}

	recent := make([]domain.Ticket, 0, len(tickets))
	for _, t := range tickets {
		if t.Workspace != w.ID {
			continue
		}
		if now.Sub(t.CreatedAt) > DigestWindow {
			continue
		}
		recent = append(recent, t)
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].CreatedAt.After(recent[j].CreatedAt) })

	var b strings.Builder
	if len(recent) == 0 {
		b.WriteString("近两周没有工单。")
	} else {
		fmt.Fprintf(&b, "近两周 %d 个工作项", len(recent))
		if ended := countEnded(recent); ended > 0 {
			fmt.Fprintf(&b, "（已完成 %d）", ended)
		}
		b.WriteString("：")
		titles := make([]string, 0, 3)
		for _, t := range recent {
			if len(titles) == 3 {
				break
			}
			if s := oneLine(t.Title, 24); s != "" {
				titles = append(titles, s)
			}
		}
		b.WriteString(strings.Join(titles, " · "))
		if len(recent) > len(titles) {
			b.WriteString(" 等")
		}
		b.WriteString("。")
	}

	// What came out is the most reliable evidence of what the workspace really is.
	if art := artifactsOf(recent); art > 0 {
		fmt.Fprintf(&b, "产出 %d 个交付物。", art)
	}
	// The directory outline, when the scanner saw it, says more than any prose.
	if dirs := dirOutline(w.Brief.Facts.DirSummary, 3); dirs != "" {
		b.WriteString("目录：" + dirs + "。")
	}
	if last := lastActivity(recent); !last.IsZero() {
		fmt.Fprintf(&b, "最近活动 %s。", humanAge(now.Sub(last)))
	}
	return clipRunes(strings.TrimSpace(b.String()), maxRunes)
}

func countEnded(ts []domain.Ticket) int {
	n := 0
	for _, t := range ts {
		if t.Ended != nil {
			n++
		}
	}
	return n
}

func artifactsOf(ts []domain.Ticket) int {
	n := 0
	for _, t := range ts {
		n += len(t.Artifacts)
	}
	return n
}

func lastActivity(ts []domain.Ticket) time.Time {
	var last time.Time
	for _, t := range ts {
		if t.CreatedAt.After(last) {
			last = t.CreatedAt
		}
		for _, a := range t.Artifacts {
			if a.At.After(last) {
				last = a.At
			}
		}
	}
	return last
}

func dirOutline(entries []domain.DirEntry, max int) string {
	if len(entries) == 0 {
		return ""
	}
	names := make([]string, 0, max)
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		name := e.Name
		if e.Summary != "" {
			name += "(" + oneLine(e.Summary, 12) + ")"
		}
		names = append(names, name)
		if len(names) == max {
			break
		}
	}
	return strings.Join(names, ", ")
}

// oneLine flattens text to a short single line, clipping through the same rune-safe
// helper as everything else in this package so the ellipsis policy has one home.
func oneLine(s string, maxRunes int) string {
	return clipRunes(strings.Join(strings.Fields(s), " "), maxRunes)
}

// clipRunes cuts on a rune boundary and marks the cut, so a clipped digest never
// looks like a complete sentence that ended oddly.
func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max < 2 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	}
	return fmt.Sprintf("%d 天前", int(d.Hours()/24))
}
