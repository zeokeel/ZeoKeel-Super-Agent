// Package brief turns workspace briefs into the two things the Router needs:
// a small text block that stays in its context, and a keyword index that cuts
// the candidate workspaces down before any model call. See design §4.6.
package brief

import (
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// Render renders one workspace as the compact block the Router keeps in
// context. The shape is fixed so a model can rely on it; keep it near 300
// tokens by summarizing, never by dumping a file.
func Render(w domain.Workspace, now time.Time) string { return RenderWith(w, now, "") }

// RenderWith appends a live digest of recent work. The digest is computed from
// durable tickets, so it is fresher than the observed Facts and says what the
// workspace is being used for right now (design §4.6).
func RenderWith(w domain.Workspace, now time.Time, digest string) string {
	var b strings.Builder
	b.WriteString("## " + string(w.ID) + " · " + w.Name)
	if w.Remote != "" {
		b.WriteString(" · " + w.Remote)
	}
	if w.Brief.Facts.DefaultBranch != "" {
		b.WriteString(" · " + w.Brief.Facts.DefaultBranch)
	}
	if w.Brief.Facts.Language != "" {
		b.WriteString(" · " + w.Brief.Facts.Language)
	}
	b.WriteString("\n")

	line := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			b.WriteString(label + "  " + value + "\n")
		}
	}
	line("目的", w.Brief.Purpose)
	line("范围", strings.Join(w.Brief.Scope, " / "))
	line("不归", strings.Join(w.Brief.OutOfScope, "；"))
	line("关键词", strings.Join(append(append([]string{}, w.Brief.Keywords...), w.Brief.Aliases...), ", "))

	if f := w.Brief.Facts; f.BuildCmd != "" || f.TestCmd != "" {
		var cmds []string
		if f.BuildCmd != "" {
			cmds = append(cmds, "构建 "+f.BuildCmd)
		}
		if f.TestCmd != "" {
			cmds = append(cmds, "测试 "+f.TestCmd)
		}
		line("命令", strings.Join(cmds, "    "))
	}
	line("约束", strings.Join(w.Brief.Constraints, "；"))
	line("近况", digest)

	if w.Brief.Stale(now) {
		b.WriteString("⚠ 信息可能过期，动手前先 workspace_read 确认\n")
	}
	return b.String()
}

// RenderAll renders every workspace, in ID order for a stable prompt.
func RenderAll(ws []domain.Workspace, now time.Time) string {
	sorted := append([]domain.Workspace(nil), ws...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	var b strings.Builder
	for i, w := range sorted {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(Render(w, now))
	}
	return b.String()
}

// Candidate is one possible workspace for a request, with the terms that matched.
type Candidate struct {
	ID      domain.WorkspaceID
	Score   int
	Matched []string
}

// Index is the deterministic first routing step: a keyword index over briefs
// that narrows N workspaces to a few candidates before any model call. It is
// built from the briefs and rebuilt when they change, so there is one source of
// truth. See design §4.6 "路由也是两级".
type Index struct {
	terms map[string][]domain.WorkspaceID
	all   []domain.WorkspaceID
}

// NewIndex builds an index over the given workspaces.
func NewIndex(ws []domain.Workspace) *Index {
	ix := &Index{terms: make(map[string][]domain.WorkspaceID)}
	for _, w := range ws {
		ix.all = append(ix.all, w.ID)
		terms := append([]string{}, w.Brief.Keywords...)
		terms = append(terms, w.Brief.Aliases...)
		terms = append(terms, w.Name)
		terms = append(terms, w.Brief.Scope...)
		for _, t := range terms {
			for _, k := range normalize(t) {
				ix.terms[k] = append(ix.terms[k], w.ID)
			}
		}
	}
	return ix
}

// Recall returns the workspaces whose keywords appear in text, best first. A
// miss returns no candidates rather than a guess: the caller must ask a person.
func (ix *Index) Recall(text string) []Candidate {
	score := make(map[domain.WorkspaceID]int)
	matched := make(map[domain.WorkspaceID]map[string]bool)
	for _, k := range normalize(text) {
		for _, id := range ix.terms[k] {
			score[id]++
			if matched[id] == nil {
				matched[id] = make(map[string]bool)
			}
			matched[id][k] = true
		}
	}
	out := make([]Candidate, 0, len(score))
	for id, s := range score {
		var terms []string
		for t := range matched[id] {
			terms = append(terms, t)
		}
		sort.Strings(terms)
		out = append(out, Candidate{ID: id, Score: s, Matched: terms})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// normalize lowercases and splits a term on non-alphanumerics, keeping CJK runs
// whole and also emitting每个汉字 so a customer name embedded in a sentence is
// found. It is deliberately simple: recall only needs to be generous, because
// the Router decides afterwards.
func normalize(s string) []string {
	s = strings.ToLower(s)
	var out []string
	var word []rune
	flush := func() {
		if len(word) > 0 {
			out = append(out, string(word))
			word = word[:0]
		}
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			word = append(word, r)
		case isCJK(r):
			flush()
			out = append(out, string(r))
		default:
			flush()
		}
	}
	flush()
	return out
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF)
}
