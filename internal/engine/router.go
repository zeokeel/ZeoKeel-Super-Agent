package engine

// The semantic layer of Eval and the types it exchanges with the engine. The
// gate (gate.go) computes strength; the assessor only judges what an admitted
// batch asks for and where it belongs (002, design §2.1).

import (
	"context"
	"errors"

	"github.com/zeokeel/super-agent/internal/brief"
	"github.com/zeokeel/super-agent/internal/domain"
)

// Assessor is the semantic layer of Eval: the only place a model is asked "is
// this a request, and which workspace is it for". It never sees or returns a
// strength.
type Assessor interface {
	Assess(ctx context.Context, in AssessInput) (AssessOutput, error)
}

// ThreadSnapshot is the Thread an Eval judges: its Triggers, the Tickets on it
// (including ended ones) and its Decision history (001:29,33).
type ThreadSnapshot struct {
	Key        string
	TriggerIDs []domain.ID
	TicketIDs  []domain.ID
	Decisions  []domain.Decision
}

// PolicyView is the read-only part of the Policy an assessment may use: the
// rules it must judge, the accept mode and the admission facts (002:101-109).
type PolicyView struct {
	Version    string
	Rules      []domain.Rule
	AcceptMode domain.AcceptMode
	Admissions map[string]domain.AdmissionRow
	Bots       map[string]map[string]bool
}

// policyViewOf projects a Policy for an assessment.
func policyViewOf(p Policy) PolicyView {
	return PolicyView{
		Version:    p.Version,
		Rules:      append([]domain.Rule(nil), p.Rules...),
		AcceptMode: acceptMode(p),
		Admissions: p.Admissions,
		Bots:       p.Bots,
	}
}

// EnabledRules returns the rules an assessment must judge exactly once.
func (v PolicyView) EnabledRules() []domain.Rule {
	out := make([]domain.Rule, 0, len(v.Rules))
	for _, r := range v.Rules {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

// AssessInput is what the assessor sees: the batch, its Trigger, candidate
// workspaces with their rendered briefs, the Thread snapshot, every Ticket on
// the Thread and the current Policy (001:33).
type AssessInput struct {
	EvalID     domain.ID
	Trigger    domain.Trigger
	Batch      []domain.Trigger
	Candidates []brief.Candidate
	Briefs     map[domain.WorkspaceID]string
	Policy     PolicyView
	Thread     ThreadSnapshot
	Tickets    []domain.Ticket
	Decisions  []domain.Decision
}

// AssessOutput is the assessor's judgement. It carries no Strength: the gate
// owns that (002:11-16).
type AssessOutput struct {
	Kind      domain.DecisionKind
	Workspace domain.WorkspaceID
	// Rules is the judgement of every enabled rule.
	Rules   []domain.RuleHit
	Reasons []domain.Reason
	// Ask is set when the workspace cannot be chosen; the text becomes an
	// ask_user_question.
	Ask string
	// Title and WorktreeName are the Ticket's title and short directory name.
	Title        string
	WorktreeName string
	// IntentEvidence is the requester's words, required on accept (002:101).
	IntentEvidence string
	// TicketID names the Ticket a stop is about, when the assessment knows it.
	TicketID domain.ID
}

// RuleAssessor is a deterministic stand-in for the model, used by tests and the
// demo. It proves the seam: replace it with a real model call and nothing else
// changes.
type RuleAssessor struct {
	// Notices are texts that are notifications, not requests.
	Notices []string
	// Rules are the rule hits the stand-in reports; nil means every enabled rule
	// in the Eval's Policy is matched (the stand-in always finds work).
	Rules []domain.RuleHit
}

// Assess implements Assessor.
func (r RuleAssessor) Assess(_ context.Context, in AssessInput) (AssessOutput, error) {
	for _, n := range r.Notices {
		if n == in.Trigger.Text {
			return AssessOutput{Kind: domain.DecIgnore,
				Reasons: []domain.Reason{{Code: domain.ReasonNotification}}}, nil
		}
	}
	rules := r.Rules
	if rules == nil {
		for _, rule := range in.Policy.EnabledRules() {
			rules = append(rules, domain.RuleHit{ID: rule.ID, Matched: true, Note: "stand-in"})
		}
	}
	if in.Trigger.DefaultWorkspace != "" && workspaceCandidate(in, in.Trigger.DefaultWorkspace) {
		return AssessOutput{Kind: domain.DecAccept, Workspace: in.Trigger.DefaultWorkspace,
			Rules: rules, Reasons: []domain.Reason{{Code: domain.ReasonSaid}},
			Title: firstSentence(in.Trigger.Text)}, nil
	}
	var best []brief.Candidate
	top := 0
	for _, c := range in.Candidates {
		if c.Score > top {
			top = c.Score
			best = []brief.Candidate{c}
		} else if c.Score == top && top > 0 {
			best = append(best, c)
		}
	}
	switch {
	case len(best) == 1:
		return AssessOutput{Kind: domain.DecAccept, Workspace: best[0].ID, Rules: rules,
			Reasons: []domain.Reason{{Code: domain.ReasonSaid, Values: map[string]string{"matched": best[0].Matched[0]}}},
			Title:   firstSentence(in.Trigger.Text)}, nil
	case len(best) > 1:
		return AssessOutput{Ask: "命中多个工作区，请指定一个"}, nil
	default:
		if in.Trigger.DefaultWorkspace != "" {
			return AssessOutput{Kind: domain.DecAccept, Workspace: in.Trigger.DefaultWorkspace,
				Rules: rules, Reasons: []domain.Reason{{Code: domain.ReasonSaid}},
				Title: firstSentence(in.Trigger.Text)}, nil
		}
		return AssessOutput{Ask: "没有匹配到工作区，请指定一个"}, nil
	}
}

// workspaceCandidate reports whether a workspace id appears among the candidates
// or the Tickets of the input.
func workspaceCandidate(in AssessInput, id domain.WorkspaceID) bool {
	for _, c := range in.Candidates {
		if c.ID == id {
			return true
		}
	}
	for _, t := range in.Tickets {
		if t.Workspace == id {
			return true
		}
	}
	return false
}

// ErrNoWorkspace reports that a request could not be routed.
var ErrNoWorkspace = errors.New("engine: no workspace matched")
