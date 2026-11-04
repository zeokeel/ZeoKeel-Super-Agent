package engine

// The deterministic authorization layer (prototype docs/002): the gate decides
// whether a Trigger may originate work and with what strength, without asking a
// model. Everything a model says is only the semantic judgement of what the
// admitted Trigger asks for; it can never upgrade a Trigger's strength.
//
// Two layers are kept apart so a later revalidation can re-derive the same
// conclusion from the same facts:
//
//	gate(trigger, policy) -> strength ∈ {explicit, inferred, not_admitted} + basis
//	revalidate(mandate, policy) -> does the current Policy still hold it?

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zeokeel/super-agent/internal/domain"
)

// Policy is one workspace's part of the rules: who may ask for work there, the
// admission table, the team's proactive rules and the accept mode (002:93-109).
type Policy struct {
	Version string
	// Allowed maps a Source name to the actor ids on that Source who may
	// originate work. Identities are never mapped across Sources.
	Allowed map[string]map[string]bool
	// Rules are the team's proactive-work rules (002:101-105). Every enabled rule
	// is judged exactly once per Eval; a proactive accept needs one match.
	Rules []domain.Rule
	// Admissions is the admission table, keyed "<source>|<row>" (002:58-71).
	Admissions map[string]domain.AdmissionRow
	// Bots is each Source's bot list: bots whose messages may become inferred
	// candidates, and only inferred (002:79).
	Bots map[string]map[string]bool
	// Listened is the Source instance's fact that it still listens to a scope,
	// keyed "<source>|<row>" (002:119). Absent means listened.
	Listened map[string]bool
	// AdmitsUnaddressed is the pre-table switch; it is read only when no
	// admission row exists for the receiving scope.
	AdmitsUnaddressed bool
	// AcceptMode decides whether an inferred candidate may start work. Empty
	// means observe: it is recorded and nothing starts (002:95-99).
	AcceptMode domain.AcceptMode
	// ReplyMode decides whether the agent may answer someone other than the
	// operator. Empty means notify, the safe default (design §4.10).
	ReplyMode domain.ReplyMode
}

// allowed reports whether actor may originate work through source.
func (p Policy) allowed(source, actor string) bool { return p.Allowed[source][actor] }

// listedBot reports whether actor is on the Source's bot list.
func (p Policy) listedBot(source, actor string) bool { return p.Bots[source][actor] }

// ruleEnabled reports whether one rule is enabled.
func (p Policy) ruleEnabled(id string) bool {
	for _, r := range p.Rules {
		if r.ID == id && r.Enabled {
			return true
		}
	}
	return false
}

// enabledRules returns the enabled rules in stable order.
func (p Policy) enabledRules() []domain.Rule {
	out := make([]domain.Rule, 0, len(p.Rules))
	for _, r := range p.Rules {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

// cellValue returns one admission cell exactly as configured, with the
// prototype's defaults when the row or the cell is absent (002:65-69).
func (p Policy) cellValue(source, row string, cell domain.AdmissionCell) domain.AdmissionCell {
	r, ok := p.Admissions[source+"|"+row]
	if !ok {
		switch cell {
		case "listedUnaddressed", domain.CellInferred:
			return domain.CellInferred
		default:
			return domain.CellIgnore
		}
	}
	switch cell {
	case "listedUnaddressed":
		if r.ListedUnaddressed != "" {
			return r.ListedUnaddressed
		}
		return domain.CellInferred
	case "unlistedAddressed":
		if r.UnlistedAddressed != "" {
			return r.UnlistedAddressed
		}
		return domain.CellIgnore
	case "unlistedUnaddressed":
		if r.UnlistedUnaddressed != "" {
			return r.UnlistedUnaddressed
		}
		return domain.CellIgnore
	case domain.CellExplicit:
		return domain.CellExplicit
	case domain.CellInferred:
		if r.ListedUnaddressed != "" {
			return r.ListedUnaddressed
		}
		return domain.CellInferred
	}
	return domain.CellIgnore
}

// observed reports whether the scope receives every message, unlocking the
// unaddressed cells (002:71).
func (p Policy) observed(source, row string) bool {
	r, ok := p.Admissions[source+"|"+row]
	return ok && r.Observed
}

// cellFor maps (listed, addressed) to the prototype's admission cell.
func cellFor(listed, addressed bool) domain.AdmissionCell {
	switch {
	case listed && addressed:
		return "" // no cell: an allowed sender who addresses the agent is explicit
	case listed:
		return domain.AdmissionCell("listedUnaddressed")
	case addressed:
		return domain.AdmissionCell("unlistedAddressed")
	default:
		return domain.AdmissionCell("unlistedUnaddressed")
	}
}

// strengthRank orders strengths for comparisons.
func strengthRank(s domain.Strength) int {
	switch s {
	case domain.StrengthExplicit:
		return 2
	case domain.StrengthInferred:
		return 1
	default:
		return 0
	}
}

// strongest returns the stronger of two strengths.
func strongest(a, b domain.Strength) domain.Strength {
	if strengthRank(a) >= strengthRank(b) {
		return a
	}
	return b
}

// gateResult is the deterministic verdict for one Trigger.
type gateResult struct {
	Admitted bool
	Strength domain.Strength
	Basis    domain.Basis
	Reasons  []domain.Reason
}

func notAdmitted(code string, values map[string]string) gateResult {
	return gateResult{
		Admitted: false,
		Strength: domain.StrengthNotAdmitted,
		Reasons:  []domain.Reason{{Code: code, Values: values}},
	}
}

// gate computes the verdict for one Trigger under one Policy. participating is
// true when the Trigger's primary Thread already has a live Ticket, which is
// what a follow-up needs (002:14,22-24).
func gate(p Policy, t domain.Trigger, participating bool) gateResult {
	row := t.Row
	if !t.Human {
		return machineGate(p, t, row)
	}
	listed := p.allowed(t.Source, t.Actor.ID)
	// The operator's own account, on any Source, is the strongest explicit
	// request there is; it is still recorded so the audit shows it.
	if t.Self || (t.Source == "local" && t.Actor.ID == "operator") {
		return gateResult{Admitted: true, Strength: domain.StrengthExplicit,
			Basis: domain.Basis{Via: domain.ViaOperator, Source: t.Source, Row: row}}
	}
	if listed && t.Addressed.AddressesAgent() {
		return gateResult{Admitted: true, Strength: domain.StrengthExplicit,
			Basis: domain.Basis{Via: domain.ViaAddressed, Source: t.Source, Row: row}}
	}
	cell := cellFor(listed, t.Addressed.AddressesAgent())
	// Unaddressed messages use the admission table only when the scope receives
	// every message; otherwise the scope cannot see them at all.
	receivable := t.Addressed.AddressesAgent() || t.ScopeReceivesAll || p.observed(t.Source, row)
	cellValue := domain.CellIgnore
	if receivable {
		cellValue = p.cellValue(t.Source, row, cell)
	} else if p.AdmitsUnaddressed && listed {
		cellValue = domain.CellInferred
	}
	basis := domain.Basis{Via: domain.ViaAdmission, Source: t.Source, Row: row, Cell: cellValue}

	// An explicit admission cell wins before a Thread follow-up, so a Thread's
	// Tickets never weaken an explicit request into a follow-up (002:54).
	if cellValue == domain.CellExplicit {
		return gateResult{Admitted: true, Strength: domain.StrengthExplicit, Basis: basis}
	}
	// A person's follow-up on a Thread with Tickets: explicit for a listed
	// sender, inferred otherwise, but never in `off` mode for unlisted senders.
	// Direct messages belong to one sender: others get no follow-up strength.
	directFromUnlisted := t.Addressed.Direct && !listed
	if !directFromUnlisted && (listed || acceptMode(p) != domain.AcceptOff) && participating {
		s := domain.StrengthInferred
		if listed {
			s = domain.StrengthExplicit
		}
		return gateResult{Admitted: true, Strength: s,
			Basis: domain.Basis{Via: domain.ViaFollowup, Source: t.Source, Row: row}}
	}
	if cellValue == domain.CellInferred && acceptMode(p) != domain.AcceptOff {
		return gateResult{Admitted: true, Strength: domain.StrengthInferred, Basis: basis}
	}
	if !listed {
		return notAdmitted(domain.ReasonNotAllowed, map[string]string{"why": "not_a_member"})
	}
	if receivable && cellValue == domain.CellIgnore {
		return notAdmitted(domain.ReasonAdmissionIgnor, map[string]string{"row": row})
	}
	return notAdmitted(domain.ReasonNotAddressed, nil)
}

// machineGate admits non-human events: a listed bot as an inferred candidate,
// and a senderless scope event whose cell is inferred (002:16,79,002:71).
func machineGate(p Policy, t domain.Trigger, row string) gateResult {
	bot := t.Actor.ID != "" && p.listedBot(t.Source, t.Actor.ID)
	if t.Actor.ID != "" && !bot {
		return notAdmitted("not_human", nil)
	}
	if !(t.Addressed.AddressesAgent() || t.ScopeReceivesAll || p.observed(t.Source, row)) {
		return notAdmitted("not_human", nil)
	}
	if acceptMode(p) == domain.AcceptOff {
		return notAdmitted(domain.ReasonAdmissionIgnor, map[string]string{"why": "mode_off"})
	}
	if bot {
		return gateResult{Admitted: true, Strength: domain.StrengthInferred,
			Basis: domain.Basis{Via: domain.ViaBot, Source: t.Source, Row: row}}
	}
	// A senderless system/CI event uses the last cell of its row.
	cell := p.cellValue(t.Source, row, domain.AdmissionCell("unlistedUnaddressed"))
	if cell != domain.CellInferred {
		return notAdmitted(domain.ReasonAdmissionIgnor, map[string]string{"row": row})
	}
	return gateResult{Admitted: true, Strength: domain.StrengthInferred,
		Basis: domain.Basis{Via: domain.ViaAdmission, Source: t.Source, Row: row, Cell: cell}}
}

// acceptMode returns the effective accept mode.
func acceptMode(p Policy) domain.AcceptMode {
	if p.AcceptMode == "" {
		return domain.AcceptObserve
	}
	return p.AcceptMode
}

// revalidate reports whether the current Policy still holds a Mandate
// (002:133-139). A failed revalidation revokes it; it never revives one.
func revalidate(p Policy, m domain.Mandate) bool {
	if m.Revoked != "" || m.Withdrawn != "" {
		return false
	}
	// The Source must still listen to the original scope (002:133).
	if row := m.Basis.Row; row != "" && m.Basis.Source != "" {
		if listened, ok := p.Listened[m.Basis.Source+"|"+row]; ok && !listened {
			return false
		}
	}
	rulesOn := func(ids []string) bool {
		for _, id := range ids {
			if !p.ruleEnabled(id) {
				return false
			}
		}
		return true
	}
	mode := acceptMode(p)
	observation := m.Strength == domain.StrengthInferred
	switch m.Basis.Via {
	case domain.ViaFollowup:
		// An explicit follow-up passes; an inferred one needs auto mode and its
		// matched rules still enabled.
		if m.Strength == domain.StrengthExplicit {
			return true
		}
		return mode == domain.AcceptAuto && rulesOn(m.Rules)
	case domain.ViaOperator:
		return true
	case domain.ViaBot:
		if !p.listedBot(m.Basis.Source, m.Actor.ID) {
			return false
		}
	case domain.ViaAdmission:
		if cell := m.Basis.Cell; cell != "" {
			listed := p.allowed(m.Basis.Source, m.Actor.ID)
			if cell == "unlistedAddressed" && listed {
				return true // a listed sender who addresses the agent is always explicit
			}
			nowCell := cell
			if cell == "unlistedUnaddressed" && listed {
				nowCell = "listedUnaddressed"
			}
			if nowCell == "listedUnaddressed" && !listed {
				return false
			}
			current := p.cellValue(m.Basis.Source, m.Basis.Row, nowCell)
			if strengthRank(cellStrength(current)) < strengthRank(m.Strength) {
				return false
			}
			if m.Strength == domain.StrengthExplicit {
				return true
			}
		} else if !p.allowed(m.Basis.Source, m.Actor.ID) {
			return false
		}
	case domain.ViaListed, domain.ViaAddressed:
		if !p.allowed(m.Basis.Source, m.Actor.ID) {
			return false
		}
	}
	// Inferred table/follow-up/bot Mandates also need auto mode and enabled rules.
	if observation && (m.Basis.Via == domain.ViaAdmission || m.Basis.Via == domain.ViaListed || m.Basis.Via == domain.ViaBot) {
		return mode == domain.AcceptAuto && rulesOn(m.Rules)
	}
	return true
}

// cellStrength maps an admission cell to the strength it grants.
func cellStrength(c domain.AdmissionCell) domain.Strength {
	switch c {
	case domain.CellExplicit:
		return domain.StrengthExplicit
	case domain.CellInferred:
		return domain.StrengthInferred
	default:
		return domain.StrengthNotAdmitted
	}
}

// policyVersion derives the Policy version from what authorization depends on:
// the rules, the accept mode and each Source's part (002:109). It is content
// derived, so the same configuration always names the same version.
func policyVersion(p Policy) string {
	type sourcePart struct {
		Allowed map[string]bool       `json:"allowed,omitempty"`
		Bots    map[string]bool       `json:"bots,omitempty"`
		Rows    []domain.AdmissionRow `json:"rows,omitempty"`
	}
	parts := map[string]sourcePart{}
	for source, actors := range p.Allowed {
		part := parts[source]
		part.Allowed = actors
		parts[source] = part
	}
	for source, bots := range p.Bots {
		part := parts[source]
		part.Bots = bots
		parts[source] = part
	}
	for _, row := range p.Admissions {
		source := row.Source
		part := parts[source]
		part.Rows = append(part.Rows, row)
		parts[source] = part
	}
	for source, part := range parts {
		sort.Slice(part.Rows, func(i, j int) bool { return part.Rows[i].Scope < part.Rows[j].Scope })
		parts[source] = part
	}
	rules := append([]domain.Rule(nil), p.Rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	raw, _ := json.Marshal(struct {
		Rules   []domain.Rule         `json:"rules"`
		Mode    domain.AcceptMode     `json:"mode"`
		Require bool                  `json:"requireApproval"`
		Sources map[string]sourcePart `json:"sources"`
	}{Rules: rules, Mode: acceptMode(p), Sources: parts})
	sum := sha256.Sum256(raw)
	return "pv-" + hex.EncodeToString(sum[:])[:16]
}

// ScopeSource returns the Source name part of a Mandate scope, for revalidation.
func scopeSource(s domain.Scope) string {
	parts := strings.SplitN(string(s), "|", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return string(s)
}

// mergePolicies combines the workspace policies that can see one global
// Trigger. The union is only used to find candidates; the Ticket's own
// workspace still has to admit the request before it is accepted (§4.5).
//
// The merge is CONSERVATIVE, never widening authorization by merge order
// (002: authorization is per Source and per scope; combining workspaces must not
// grant more than any one of them):
//
//   - overlapping admission cells: the stricter cell wins (ignore > inferred > explicit);
//     a row seen as not-observed by any workspace keeps the unaddressed cells closed;
//   - accept mode: stricter wins (off > observe > auto);
//   - reply mode: stricter wins (notify > assist > auto);
//   - allowed lists and bot lists: union (per-workspace re-gate caps authority);
//   - rules: union deduped by id; a rule disabled anywhere counts as disabled.
func mergePolicies(policies []Policy, names []string) Policy {
	out := Policy{
		Allowed:    map[string]map[string]bool{},
		Bots:       map[string]map[string]bool{},
		Admissions: map[string]domain.AdmissionRow{},
		Listened:   map[string]bool{},
		Rules:      nil,
	}
	ruleSeen := map[string]int{}
	mode := domain.AcceptAuto
	reply := domain.ReplyModeAuto
	for _, p := range policies {
		for source, actors := range p.Allowed {
			if out.Allowed[source] == nil {
				out.Allowed[source] = map[string]bool{}
			}
			for a := range actors {
				out.Allowed[source][a] = true
			}
		}
		for source, bots := range p.Bots {
			if out.Bots[source] == nil {
				out.Bots[source] = map[string]bool{}
			}
			for b := range bots {
				out.Bots[source][b] = true
			}
		}
		for k, row := range p.Admissions {
			if existing, ok := out.Admissions[k]; ok {
				out.Admissions[k] = stricterRow(existing, row)
			} else {
				out.Admissions[k] = row
			}
		}
		for k, v := range p.Listened {
			if existing, ok := out.Listened[k]; ok {
				out.Listened[k] = existing && v
			} else {
				out.Listened[k] = v
			}
		}
		mode = stricterAcceptMode(mode, acceptMode(p))
		reply = stricterReplyMode(reply, p.ReplyMode)
		for _, r := range p.Rules {
			if i, ok := ruleSeen[r.ID]; ok {
				out.Rules[i].Enabled = out.Rules[i].Enabled && r.Enabled
				out.Rules[i].Proactive = out.Rules[i].Proactive && r.Proactive
				continue
			}
			ruleSeen[r.ID] = len(out.Rules)
			out.Rules = append(out.Rules, r)
		}
	}
	sort.Strings(names)
	out.AcceptMode = mode
	out.ReplyMode = reply
	out.Version = fmt.Sprintf("merged-%s", strings.Join(names, "+"))
	return out
}

// cellRank orders admission cells by restrictiveness: ignore is strictest.
func cellRank(c domain.AdmissionCell) int {
	switch c {
	case domain.CellIgnore:
		return 3
	case domain.CellInferred:
		return 2
	case domain.CellExplicit:
		return 1
	default:
		return 0
	}
}

// stricterCell returns the more restrictive of two cells (ignore > inferred > explicit).
func stricterCell(a, b domain.AdmissionCell) domain.AdmissionCell {
	if cellRank(a) >= cellRank(b) {
		return a
	}
	return b
}

// resolvedCell returns the cell's effective value, applying the prototype defaults.
func resolvedCell(row domain.AdmissionRow, which domain.AdmissionCell) domain.AdmissionCell {
	var cfg domain.AdmissionCell
	switch which {
	case "listedUnaddressed":
		cfg = row.ListedUnaddressed
		if cfg == "" {
			return domain.CellInferred
		}
	case "unlistedAddressed":
		cfg = row.UnlistedAddressed
		if cfg == "" {
			return domain.CellIgnore
		}
	default:
		cfg = row.UnlistedUnaddressed
		if cfg == "" {
			return domain.CellIgnore
		}
	}
	return cfg
}

// stricterRow merges two rows of the same receiving scope conservatively.
func stricterRow(a, b domain.AdmissionRow) domain.AdmissionRow {
	out := a
	out.Observed = a.Observed && b.Observed
	out.ListedUnaddressed = stricterCell(resolvedCell(a, "listedUnaddressed"), resolvedCell(b, "listedUnaddressed"))
	out.UnlistedAddressed = stricterCell(resolvedCell(a, "unlistedAddressed"), resolvedCell(b, "unlistedAddressed"))
	out.UnlistedUnaddressed = stricterCell(resolvedCell(a, "unlistedUnaddressed"), resolvedCell(b, "unlistedUnaddressed"))
	return out
}

// stricterAcceptMode orders accept modes by strictness: off > observe > auto.
func stricterAcceptMode(a, b domain.AcceptMode) domain.AcceptMode {
	if a == "" {
		a = domain.AcceptObserve
	}
	if b == "" {
		b = domain.AcceptObserve
	}
	return domain.AcceptMode(stricterMode(string(a), string(b), map[string]int{
		string(domain.AcceptOff): 3, string(domain.AcceptObserve): 2, string(domain.AcceptAuto): 1,
	}))
}

// stricterReplyMode orders reply modes by strictness: notify > assist > auto.
func stricterReplyMode(a, b domain.ReplyMode) domain.ReplyMode {
	if a == "" {
		a = domain.ReplyModeNotify
	}
	if b == "" {
		b = domain.ReplyModeNotify
	}
	return domain.ReplyMode(stricterMode(string(a), string(b), map[string]int{
		string(domain.ReplyModeNotify): 3, string(domain.ReplyModeAssist): 2, string(domain.ReplyModeAuto): 1,
	}))
}

// stricterMode returns the value with the higher strictness rank; an empty value
// falls back to its default rank.
func stricterMode(a, b string, rank map[string]int) string {
	if rank[a] >= rank[b] {
		return a
	}
	return b
}
