package view

// Cross-area search: one query over the projections the window already holds, so
// the palette answers without touching a database, a file or the network (design
// §6.4: CommandPalette = Modal + SearchField + List; §6.6: 零延迟).
//
// BuildSearch is a pure function of published data and the query. The page calls
// it on the main thread when the query or a projection changes and caches the
// result for the frame, exactly as the ticket and decision filters do: a view
// never searches while drawing. A runtime that owns more history may publish a
// SearchVM of its own for the current query; the page prefers that projection
// when it is there (Snapshot.PublishSearch).

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/humanfmt"
)

// SearchKind groups hits by what they are, so a title never has to say where it
// came from (design §6.3: 用领域术语).
type SearchKind string

const (
	SearchTickets    SearchKind = "tickets"    // 工单标题
	SearchMessages   SearchKind = "messages"   // 消息文本
	SearchDecisions  SearchKind = "decisions"  // Decision 摘要
	SearchWorkspaces SearchKind = "workspaces" // 工作区名
	SearchSources    SearchKind = "sources"    // 来源名
)

// Label is how a group reads in Chinese. The page shows this, never the raw value.
func (k SearchKind) Label() string {
	switch k {
	case SearchTickets:
		return "工单"
	case SearchMessages:
		return "消息"
	case SearchDecisions:
		return "决策"
	case SearchWorkspaces:
		return "工作区"
	case SearchSources:
		return "来源"
	}
	return string(k)
}

// SearchHit is one result: the line the palette shows, and everything the page
// needs to jump to it and locate it. The IDs are strings so view stays leaf-level.
type SearchHit struct {
	Kind SearchKind
	// Title is the matched line, clipped for the palette.
	Title string
	// Detail is one line of context: workspace, source, reason code.
	Detail string
	// Section is the page Enter jumps to.
	Section Section

	// The fields below locate the hit on that page. Exactly the ones of Kind are
	// set: a ticket's ID, a Decision's, the workspace a work item belongs to, the
	// Source kind, or the exact message text (a stream head line has no ID).
	TicketID   string
	DecisionID string
	Workspace  string
	Source     string
	Text       string

	// Score ranks hits inside their group: 2 for a prefix match, 1 for a match
	// further in. The palette does not show it.
	Score int
}

// SearchGroup is one heading of the palette: a kind, its label and its hits,
// best first. An empty group is never added, so the heading always has rows.
type SearchGroup struct {
	Kind  SearchKind
	Label string
	Hits  []SearchHit
}

// SearchVM is what the palette draws for one query.
type SearchVM struct {
	Query  string
	Groups []SearchGroup
	// Total counts every match, before SearchGroupLimit clipped the groups; the
	// palette says "只显示前 N 条" while Total exceeds what it shows (005
	// "诚实状态"). Truncated is that fact as a bool.
	Total     int
	Truncated bool
	UpdatedAt time.Time
}

// SearchGroupLimit bounds one group: the palette is a jump list, not a report,
// and a bounded list keeps a frame cheap while the operator types.
const SearchGroupLimit = 20

// Empty reports whether the query matched nothing.
func (vm *SearchVM) Empty() bool {
	return vm == nil || vm.Total == 0
}

// Flat returns the hits in display order, so the keyboard moves over one slice
// and the group headings are never selectable.
func (vm *SearchVM) Flat() []SearchHit {
	if vm == nil {
		return nil
	}
	out := make([]SearchHit, 0, vm.Total)
	for _, g := range vm.Groups {
		out = append(out, g.Hits...)
	}
	return out
}

// Group returns one group by kind, or nil when it has no hits.
func (vm *SearchVM) Group(kind SearchKind) *SearchGroup {
	if vm == nil {
		return nil
	}
	for i := range vm.Groups {
		if vm.Groups[i].Kind == kind {
			return &vm.Groups[i]
		}
	}
	return nil
}

// SearchInput is the published projections a query searches. Nil pages are
// skipped: a page that has not published yet contributes no hits instead of
// failing the search.
type SearchInput struct {
	Tickets   *TicketListVM
	Stream    *StreamVM
	Decisions *DecisionVM
	Sources   *SourceVM
}

// BuildSearch searches the projections the window holds. The query is matched
// case-insensitively as a substring; a hit starting with the query ranks before
// one that only contains it. Groups are ordered work first, then what it came
// from, and each group is
// clipped to SearchGroupLimit while Total keeps the honest count.
func BuildSearch(query string, in SearchInput) *SearchVM {
	vm := &SearchVM{Query: query}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return vm
	}

	add := func(kind SearchKind, hits []SearchHit) {
		if len(hits) == 0 {
			return
		}
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
		vm.Total += len(hits)
		if len(hits) > SearchGroupLimit {
			vm.Truncated = true
			hits = hits[:SearchGroupLimit]
		}
		for i := range hits {
			hits[i].Score = 0
		}
		vm.Groups = append(vm.Groups, SearchGroup{Kind: kind, Label: kind.Label(), Hits: hits})
	}

	add(SearchTickets, ticketHits(q, in.Tickets))
	add(SearchMessages, messageHits(q, in.Stream))
	add(SearchDecisions, decisionHits(q, in.Decisions))
	add(SearchWorkspaces, workspaceHits(q, in.Tickets, in.Decisions))
	add(SearchSources, sourceHits(q, in.Sources))
	return vm
}

func ticketHits(q string, vm *TicketListVM) []SearchHit {
	if vm == nil {
		return nil
	}
	var out []SearchHit
	for _, r := range vm.Rows {
		score := matchScore(q, r.Title, "#"+r.ID)
		if score == 0 {
			continue
		}
		out = append(out, SearchHit{
			Kind:      SearchTickets,
			Title:     humanfmt.Clip(r.Title, 120),
			Detail:    fmt.Sprintf("%s · %s · %s", TicketLabel(r.ID, r.Number), r.Workspace, r.Stage),
			Section:   SecExecution,
			TicketID:  r.ID,
			Workspace: r.Workspace,
			Score:     score,
		})
	}
	return out
}

func messageHits(q string, vm *StreamVM) []SearchHit {
	if vm == nil {
		return nil
	}
	var out []SearchHit
	for _, th := range vm.Threads {
		head := th.Head
		if matchScore(q, head.Text) == 0 {
			continue
		}
		detail := strings.TrimSpace(strings.Trim(head.Source+" · "+head.Who, " ·"))
		out = append(out, SearchHit{
			Kind:    SearchMessages,
			Title:   humanfmt.Clip(head.Text, 120),
			Detail:  detail,
			Section: SecMessages,
			Text:    head.Text,
			Score:   matchScore(q, head.Text),
		})
	}
	return out
}

func decisionHits(q string, vm *DecisionVM) []SearchHit {
	if vm == nil {
		return nil
	}
	var out []SearchHit
	for _, r := range vm.Rows {
		score := matchScore(q, r.Summary, r.Trigger)
		if score == 0 {
			continue
		}
		title := r.Summary
		if title == "" {
			title = r.Trigger
		}
		detail := strings.TrimSpace(r.Trigger + " · " + r.ReasonCode)
		out = append(out, SearchHit{
			Kind:       SearchDecisions,
			Title:      humanfmt.Clip(title, 120),
			Detail:     humanfmt.Clip(detail, 140),
			Section:    SecEval,
			DecisionID: r.ID,
			Workspace:  r.Workspace,
			Score:      score,
		})
	}
	return out
}

// workspaceAgg is the two counts a workspace hit shows: work items and Decisions
// that name it.
type workspaceAgg struct {
	tickets   int
	decisions int
}

func workspaceHits(q string, tickets *TicketListVM, decisions *DecisionVM) []SearchHit {
	if tickets == nil && decisions == nil {
		return nil
	}
	counts := map[string]*workspaceAgg{}
	order := []string{}
	touch := func(name string) *workspaceAgg {
		if name == "" {
			return nil
		}
		agg, ok := counts[name]
		if !ok {
			agg = &workspaceAgg{}
			counts[name] = agg
			order = append(order, name)
		}
		return agg
	}
	if tickets != nil {
		for _, r := range tickets.Rows {
			if agg := touch(r.Workspace); agg != nil {
				agg.tickets++
			}
		}
	}
	if decisions != nil {
		for _, r := range decisions.Rows {
			if agg := touch(r.Workspace); agg != nil {
				agg.decisions++
			}
		}
	}

	var out []SearchHit
	for _, name := range order {
		score := matchScore(q, name)
		if score == 0 {
			continue
		}
		agg := counts[name]
		out = append(out, SearchHit{
			Kind:      SearchWorkspaces,
			Title:     name,
			Detail:    fmt.Sprintf("%d 个任务 · %d 个决策", agg.tickets, agg.decisions),
			Section:   SecExecution,
			Workspace: name,
			Score:     score,
		})
	}
	return out
}

func sourceHits(q string, vm *SourceVM) []SearchHit {
	if vm == nil {
		return nil
	}
	var out []SearchHit
	for _, s := range vm.Sources {
		score := matchScore(q, s.Name, s.Kind, s.Account)
		if score == 0 {
			continue
		}
		state := "已启用"
		if !s.Enabled {
			state = "已停用"
		}
		out = append(out, SearchHit{
			Kind:    SearchSources,
			Title:   s.Name,
			Detail:  humanfmt.Clip(s.Account+" · "+state, 140),
			Section: SecSource,
			Source:  s.Kind,
			Score:   score,
		})
	}
	return out
}

// matchScore ranks a hit: 2 when one of the fields starts with the query, 1 when
// one contains it, 0 when none do. Fields are checked from most to least
// specific, so a title prefix outranks an ID match.
func matchScore(q string, fields ...string) int {
	best := 0
	for _, f := range fields {
		if f == "" {
			continue
		}
		lower := strings.ToLower(f)
		switch {
		case strings.HasPrefix(lower, q):
			best = 2
		case strings.Contains(lower, q):
			if best == 0 {
				best = 1
			}
		}
	}
	return best
}
