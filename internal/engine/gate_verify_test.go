package engine

// Independent verification (verifier role): deterministic authorization.
// This file was written against _ref/docs/002-authorization.md and the audited
// claims in audit/fixes/fix-engine-api.md; it does not import or modify the
// fix-engine tests. Failing cases here are repros for the owner (conf-domain).

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// ---- independent fixtures ---------------------------------------------------

func vfRepo(id domain.WorkspaceID) domain.Workspace {
	return domain.Workspace{ID: id, Name: string(id), Root: "/tmp/" + string(id),
		Kind: domain.KindRepo, Brief: domain.Brief{Purpose: "web app", Keywords: []string{"webapp"}}}
}

func vfPolicy(mode domain.AcceptMode) Policy {
	return Policy{
		Version: "v1",
		Rules: []domain.Rule{
			{ID: "implementation", Instruction: "要求改代码", Enabled: true, Proactive: true},
		},
		Allowed: map[string]map[string]bool{"github": {"lin": true}},
		Admissions: map[string]domain.AdmissionRow{
			"github|comment": {Source: "github", Scope: "comment", Observed: true,
				ListedUnaddressed:   domain.CellInferred,
				UnlistedAddressed:   domain.CellIgnore,
				UnlistedUnaddressed: domain.CellIgnore},
		},
		Bots:       map[string]map[string]bool{"github": {"dependabot[bot]": true}},
		AcceptMode: mode,
	}
}

// vfTrigger builds a human or bot trigger; addressed is mention-style.
func vfTrigger(id, actor string, human, addressed, receivesAll bool) domain.Trigger {
	return domain.Trigger{
		ID:               domain.Key(domain.Global, "vf", id),
		Scope:            domain.Global,
		Source:           "github",
		Threads:          []domain.ThreadRef{{Source: "github", ID: "owner/repo#12"}},
		Actor:            domain.Actor{ID: actor, Bot: !human},
		Human:            human,
		Addressed:        domain.Addressed{Mention: addressed},
		Row:              "comment",
		ScopeReceivesAll: receivesAll,
		Text:             "webapp: change the timeout",
		At:               time.Now(),
	}
}

func vfThreadKey(t domain.Trigger) string {
	return threadKeyOf(&t)
}

// vfUnobservedPolicy is an admission row with Observed=false: the scope does not
// receive every message, so the two unaddressed cells are unavailable (002:71).
//
// It exists because an earlier inline copy of this fixture was named "unobserved"
// while leaving the base row's Observed=true (verifier-B caught that: the case
// passed for the wrong reason). Never inline this again; the fixture guard test
// below fails if the helper regresses.
func vfUnobservedPolicy(mode domain.AcceptMode) Policy {
	p := vfPolicy(mode)
	row := p.Admissions["github|comment"]
	row.Observed = false
	p.Admissions["github|comment"] = row
	return p
}

// ---- check 1: gate matrix (self/allowed/unlisted/bot x addressed x cells x modes)

func TestVerifyGateMatrix(t *testing.T) {
	type modeWant map[domain.AcceptMode]domain.Strength
	type c struct {
		name          string
		policy        func(domain.AcceptMode) Policy // mode-dependent policy edits
		trigger       func(domain.AcceptMode) domain.Trigger
		participating bool
		want          modeWant // missing mode means: every mode expects this value
	}
	const (
		explicit = domain.StrengthExplicit
		inferred = domain.StrengthInferred
		denied   = domain.StrengthNotAdmitted
	)
	cases := []c{
		{
			name:   "self account is explicit even unlisted and unaddressed",
			policy: vfPolicy,
			trigger: func(domain.AcceptMode) domain.Trigger {
				tr := vfTrigger("self", "stranger", true, false, false)
				tr.Self = true
				return tr
			},
			want: modeWant{domain.AcceptOff: explicit, domain.AcceptObserve: explicit, domain.AcceptAuto: explicit},
		},
		{
			name:   "local operator is explicit",
			policy: vfPolicy,
			trigger: func(domain.AcceptMode) domain.Trigger {
				tr := vfTrigger("operator", "operator", true, false, false)
				tr.Source = "local"
				return tr
			},
			want: modeWant{domain.AcceptOff: explicit, domain.AcceptObserve: explicit, domain.AcceptAuto: explicit},
		},
		{
			name:    "listed+addressed is explicit in every accept mode",
			policy:  vfPolicy,
			trigger: func(domain.AcceptMode) domain.Trigger { return vfTrigger("listed-addr", "lin", true, true, true) },
			want:    modeWant{domain.AcceptOff: explicit, domain.AcceptObserve: explicit, domain.AcceptAuto: explicit},
		},
		{
			name:          "listed follow-up on a Thread with a Ticket is explicit",
			policy:        vfPolicy,
			trigger:       func(domain.AcceptMode) domain.Trigger { return vfTrigger("listed-follow", "lin", true, false, false) },
			participating: true,
			want:          modeWant{domain.AcceptOff: explicit, domain.AcceptObserve: explicit, domain.AcceptAuto: explicit},
		},
		{
			name:    "listed unaddressed inferred cell: inferred except in off",
			policy:  vfPolicy,
			trigger: func(domain.AcceptMode) domain.Trigger { return vfTrigger("lu-inf", "lin", true, false, true) },
			want:    modeWant{domain.AcceptOff: denied, domain.AcceptObserve: inferred, domain.AcceptAuto: inferred},
		},
		{
			name: "listed unaddressed ignore cell is not admitted",
			policy: func(mode domain.AcceptMode) Policy {
				p := vfPolicy(mode)
				row := p.Admissions["github|comment"]
				row.ListedUnaddressed = domain.CellIgnore
				p.Admissions["github|comment"] = row
				return p
			},
			trigger: func(domain.AcceptMode) domain.Trigger { return vfTrigger("lu-ignore", "lin", true, false, true) },
			want:    modeWant{domain.AcceptOff: denied, domain.AcceptObserve: denied, domain.AcceptAuto: denied},
		},
		{
			name: "unobserved scope loses the unaddressed cells",
			policy: func(mode domain.AcceptMode) Policy {
				p := vfUnobservedPolicy(mode)
				row := p.Admissions["github|comment"]
				row.ListedUnaddressed = domain.CellInferred
				p.Admissions["github|comment"] = row
				return p
			},
			trigger: func(domain.AcceptMode) domain.Trigger { return vfTrigger("unobs", "lin", true, false, false) },
			want:    modeWant{domain.AcceptOff: denied, domain.AcceptObserve: denied, domain.AcceptAuto: denied},
		},
		{
			name: "unlisted addressed explicit cell wins in every mode",
			policy: func(mode domain.AcceptMode) Policy {
				p := vfPolicy(mode)
				row := p.Admissions["github|comment"]
				row.UnlistedAddressed = domain.CellExplicit
				p.Admissions["github|comment"] = row
				return p
			},
			trigger: func(domain.AcceptMode) domain.Trigger { return vfTrigger("ua-explicit", "stranger", true, true, true) },
			want:    modeWant{domain.AcceptOff: explicit, domain.AcceptObserve: explicit, domain.AcceptAuto: explicit},
		},
		{
			name: "unlisted addressed inferred cell depends on accept mode",
			policy: func(mode domain.AcceptMode) Policy {
				p := vfPolicy(mode)
				row := p.Admissions["github|comment"]
				row.UnlistedAddressed = domain.CellInferred
				p.Admissions["github|comment"] = row
				return p
			},
			trigger: func(domain.AcceptMode) domain.Trigger { return vfTrigger("ua-inferred", "stranger", true, true, true) },
			want:    modeWant{domain.AcceptOff: denied, domain.AcceptObserve: inferred, domain.AcceptAuto: inferred},
		},
		{
			name: "unlisted addressed ignore cell is not admitted",
			policy: func(mode domain.AcceptMode) Policy {
				p := vfPolicy(mode)
				row := p.Admissions["github|comment"]
				row.UnlistedAddressed = domain.CellIgnore
				p.Admissions["github|comment"] = row
				return p
			},
			trigger: func(domain.AcceptMode) domain.Trigger { return vfTrigger("ua-ignore", "stranger", true, true, true) },
			want:    modeWant{domain.AcceptAuto: denied},
		},
		{
			name: "unlisted unaddressed inferred cell (observed scope)",
			policy: func(mode domain.AcceptMode) Policy {
				p := vfPolicy(mode)
				row := p.Admissions["github|comment"]
				row.UnlistedUnaddressed = domain.CellInferred
				p.Admissions["github|comment"] = row
				return p
			},
			trigger: func(domain.AcceptMode) domain.Trigger { return vfTrigger("uu-inferred", "stranger", true, false, true) },
			want:    modeWant{domain.AcceptOff: denied, domain.AcceptObserve: inferred, domain.AcceptAuto: inferred},
		},
		{
			name:   "unlisted follow-up is inferred, and must not be admitted in off",
			policy: vfPolicy,
			trigger: func(domain.AcceptMode) domain.Trigger {
				return vfTrigger("unlisted-follow", "stranger", true, true, false)
			},
			participating: true,
			want:          modeWant{domain.AcceptOff: denied, domain.AcceptObserve: inferred, domain.AcceptAuto: inferred},
		},
		{
			name:   "listed bot that addresses the agent is inferred",
			policy: vfPolicy,
			trigger: func(domain.AcceptMode) domain.Trigger {
				return vfTrigger("bot-addr", "dependabot[bot]", false, true, false)
			},
			want: modeWant{domain.AcceptOff: denied, domain.AcceptObserve: inferred, domain.AcceptAuto: inferred},
		},
		{
			name:   "listed bot in a receive-all scope is inferred",
			policy: vfPolicy,
			trigger: func(domain.AcceptMode) domain.Trigger {
				return vfTrigger("bot-all", "dependabot[bot]", false, false, true)
			},
			want: modeWant{domain.AcceptOff: denied, domain.AcceptObserve: inferred, domain.AcceptAuto: inferred},
		},
		{
			name: "listed bot in an unobserved scope is not admitted",
			policy: func(mode domain.AcceptMode) Policy {
				return vfUnobservedPolicy(mode)
			},
			trigger: func(domain.AcceptMode) domain.Trigger {
				return vfTrigger("bot-unobs", "dependabot[bot]", false, false, false)
			},
			want: modeWant{domain.AcceptOff: denied, domain.AcceptObserve: denied, domain.AcceptAuto: denied},
		},
		{
			name:   "unlisted bot is not admitted",
			policy: vfPolicy,
			trigger: func(domain.AcceptMode) domain.Trigger {
				return vfTrigger("bot-unlisted", "random[bot]", false, true, true)
			},
			want: modeWant{domain.AcceptAuto: denied},
		},
		{
			name:   "a bot claiming Self is still never explicit",
			policy: vfPolicy,
			trigger: func(domain.AcceptMode) domain.Trigger {
				tr := vfTrigger("bot-self", "dependabot[bot]", false, true, true)
				tr.Self = true
				return tr
			},
			want: modeWant{domain.AcceptAuto: inferred},
		},
	}
	modes := []domain.AcceptMode{domain.AcceptOff, domain.AcceptObserve, domain.AcceptAuto}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range modes {
				want, ok := tc.want[mode]
				if !ok {
					// Single-mode expectation: apply it only if it is the policy's mode.
					continue
				}
				p := tc.policy(mode)
				tr := tc.trigger(mode)
				g := gate(p, tr, tc.participating)
				if g.Strength != want {
					t.Errorf("mode=%s strength=%q want=%q admitted=%v reasons=%v",
						mode, g.Strength, want, g.Admitted, g.Reasons)
				}
				if want == denied && g.Admitted {
					t.Errorf("mode=%s: not-admitted trigger was admitted", mode)
				}
				// Determinism: the same facts must always give the same verdict.
				if again := gate(p, tr, tc.participating); again.Strength != g.Strength || again.Admitted != g.Admitted {
					t.Errorf("mode=%s: gate is not deterministic: %+v vs %+v", mode, g, again)
				}
			}
		})
	}
}

// A bot can never produce explicit strength, whatever cells/modes claim.
func TestVerifyBotNeverExplicit(t *testing.T) {
	for _, mode := range []domain.AcceptMode{domain.AcceptOff, domain.AcceptObserve, domain.AcceptAuto} {
		for _, addressed := range []bool{false, true} {
			for _, receivesAll := range []bool{false, true} {
				for _, listed := range []bool{false, true} {
					p := vfPolicy(mode)
					if !listed {
						p.Bots = map[string]map[string]bool{}
					}
					tr := vfTrigger(fmt.Sprintf("bot-%v-%v-%v-%v", mode, addressed, receivesAll, listed),
						"dependabot[bot]", false, addressed, receivesAll)
					if !listed {
						tr.Actor.ID = "random[bot]"
					}
					g := gate(p, tr, false)
					if g.Strength == domain.StrengthExplicit {
						t.Fatalf("bot got explicit strength: mode=%s addressed=%v receivesAll=%v listed=%v (%+v)",
							mode, addressed, receivesAll, listed, g)
					}
				}
			}
		}
	}
}

// The model's output type must not be able to carry a strength at all: the gate
// owns it (002:11-16 / fix-engine-api.md §0.4).
func TestVerifyAssessOutputHasNoStrength(t *testing.T) {
	if f, ok := reflect.TypeOf(AssessOutput{}).FieldByName("Strength"); ok {
		t.Fatalf("AssessOutput still has a Strength field at index %v: a model could claim authorization", f.Index)
	}
	if f, ok := reflect.TypeOf(AssessInput{}).FieldByName("Strength"); ok {
		t.Fatalf("AssessInput exposes Strength (%v); the judge does not need it", f.Index)
	}
}

// ---- check 1b: a model judgement cannot override the gate -------------------

// vfAssessor returns a fixed judgement and records the input.
type vfAssessor struct {
	out   AssessOutput
	calls int
	last  AssessInput
}

func (a *vfAssessor) Assess(_ context.Context, in AssessInput) (AssessOutput, error) {
	a.calls++
	a.last = in
	return a.out, nil
}

func vfAccepting(out AssessOutput) *vfAssessor {
	return &vfAssessor{out: out}
}

// An inferred candidate whose assessor picks another workspace with an explicit
// admission cell must stay inferred: a judgement can downgrade, never upgrade.
func TestVerifyModelCannotUpgradeStrength(t *testing.T) {
	pA := vfPolicy(domain.AcceptAuto)
	row := pA.Admissions["github|comment"]
	row.UnlistedAddressed = domain.CellInferred
	pA.Admissions["github|comment"] = row

	pB := vfPolicy(domain.AcceptAuto)
	rowB := pB.Admissions["github|comment"]
	rowB.UnlistedAddressed = domain.CellExplicit
	pB.Admissions["github|comment"] = rowB

	a := vfAccepting(AssessOutput{
		Kind: domain.DecAccept, Workspace: "ws_b",
		Rules: []domain.RuleHit{{ID: "implementation", Matched: true}},
		Title: "upgrade attempt",
	})
	e := New(a)
	e.Register(vfRepo("ws_a"), pA)
	e.Register(vfRepo("ws_b"), pB)

	tr := vfTrigger("upgrade", "stranger", true, true, true)
	// The Trigger is bound to ws_a, so the gate that decides its strength uses the
	// ws_a policy deterministically; the model then names ws_b.
	tr.Scope = domain.WorkspaceScope("ws_a")
	tr.DefaultWorkspace = "ws_a"
	if _, err := e.Receive(tr); err != nil {
		t.Fatal(err)
	}
	outs, err := e.EvaluateDue(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var tk *domain.Ticket
	for _, o := range outs {
		if o.Ticket != "" {
			if got, ok := e.TicketSnapshot(o.Ticket); ok {
				tk = &got
			}
		}
	}
	if tk == nil {
		t.Fatalf("no ticket from %+v", outs)
	}
	if tk.Mandate.Strength != domain.StrengthInferred {
		t.Fatalf("the assessor upgraded inferred to %q by naming a permissive workspace", tk.Mandate.Strength)
	}
}

// An explicit candidate whose assessor picks a workspace that refuses it must not
// create a Ticket there (the target workspace authorizes its own Ticket).
func TestVerifyModelCannotWidenWorkspaceAuthorization(t *testing.T) {
	pA := vfPolicy(domain.AcceptAuto)
	pB := vfPolicy(domain.AcceptAuto)
	pB.Allowed = map[string]map[string]bool{} // ws_b admits nobody
	a := vfAccepting(AssessOutput{
		Kind: domain.DecAccept, Workspace: "ws_b",
		Rules: []domain.RuleHit{{ID: "implementation", Matched: true}},
		Title: "narrow attempt",
	})
	e := New(a)
	e.Register(vfRepo("ws_a"), pA)
	e.Register(vfRepo("ws_b"), pB)
	if _, err := e.Receive(vfTrigger("narrow", "lin", true, true, true)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := e.AllTickets(); len(got) != 0 {
		t.Fatalf("ws_b refused the sender, but the model opened a ticket there: %+v", got)
	}
}

// A global Trigger sees every workspace's policy through mergePolicies. The merged
// view must be deterministic: the same policies must always give the same gate
// verdict, independent of Go's randomized map iteration.
func TestVerifyMergedPolicyIsDeterministic(t *testing.T) {
	pInferred := vfPolicy(domain.AcceptAuto)
	r1 := pInferred.Admissions["github|comment"]
	r1.UnlistedAddressed = domain.CellInferred
	pInferred.Admissions["github|comment"] = r1

	pExplicit := vfPolicy(domain.AcceptAuto)
	r2 := pExplicit.Admissions["github|comment"]
	r2.UnlistedAddressed = domain.CellExplicit
	pExplicit.Admissions["github|comment"] = r2

	e := New(RuleAssessor{})
	e.Register(vfRepo("ws_a"), pInferred)
	e.Register(vfRepo("ws_b"), pExplicit)

	tr := vfTrigger("merged", "stranger", true, true, true)
	seen := map[domain.Strength]int{}
	for i := 0; i < 200; i++ {
		e.mu.Lock()
		p, _ := e.policyForLocked(&tr)
		g := gate(p, tr, false)
		e.mu.Unlock()
		seen[g.Strength]++
	}
	if len(seen) > 1 {
		t.Fatalf("the merged policy is non-deterministic across calls: %v (map iteration order leaks into authorization)", seen)
	}
}

// vfFixedWorkspaceAssessor always accepts into one workspace; used to show that
// the merged-policy nondeterminism reaches the final Mandate, not just an
// intermediate value.
type vfFixedWorkspaceAssessor struct{ ws domain.WorkspaceID }

func (a vfFixedWorkspaceAssessor) Assess(_ context.Context, _ AssessInput) (AssessOutput, error) {
	// A real assessor takes its rules from the evaluation config; the hit below is
	// what a positive assessment of a proactive candidate looks like.
	return AssessOutput{Kind: domain.DecAccept, Workspace: a.ws,
		Rules:   []domain.RuleHit{{ID: "implementation", Matched: true}},
		Reasons: []domain.Reason{{Code: domain.ReasonSaid}}, Title: "x"}, nil
}

// The same global Trigger, the same two policies, the same assessor: the Mandate
// strength must not depend on which workspace the map happened to yield first.
func TestVerifyMergedPolicyChangesFinalStrength(t *testing.T) {
	pInferred := vfPolicy(domain.AcceptAuto)
	r1 := pInferred.Admissions["github|comment"]
	r1.UnlistedAddressed = domain.CellInferred
	pInferred.Admissions["github|comment"] = r1

	pExplicit := vfPolicy(domain.AcceptAuto)
	r2 := pExplicit.Admissions["github|comment"]
	r2.UnlistedAddressed = domain.CellExplicit
	pExplicit.Admissions["github|comment"] = r2

	seen := map[domain.Strength]int{}
	for i := 0; i < 60; i++ {
		e := New(vfFixedWorkspaceAssessor{ws: "ws_b"})
		e.Register(vfRepo("ws_a"), pInferred)
		e.Register(vfRepo("ws_b"), pExplicit)
		tr := vfTrigger("final-strength", "stranger", true, true, true)
		if _, err := e.Receive(tr); err != nil {
			t.Fatal(err)
		}
		if _, err := e.EvaluateDue(time.Now()); err != nil {
			t.Fatal(err)
		}
		for _, tk := range e.AllTickets() {
			seen[tk.Mandate.Strength]++
		}
	}
	t.Logf("final mandate strengths over 60 identical runs: %v", seen)
	if len(seen) > 1 {
		t.Fatalf("identical inputs produced different Mandate strengths: %v (merged policy depends on map iteration order)", seen)
	}
}

// vfRecordingAssessor records what the judge was given.
type vfRecordingAssessor struct {
	out  AssessOutput
	last AssessInput
}

func (a *vfRecordingAssessor) Assess(_ context.Context, in AssessInput) (AssessOutput, error) {
	a.last = in
	return a.out, nil
}

// A global Source has no workspace until the Router decides, so the Eval runs with
// a merged policy. The judge must still receive that Policy (rules/version/mode),
// otherwise a real assessment is blind and RuleAssessor cannot judge rules.
func TestVerifyGlobalTriggerAssessmentSeesPolicy(t *testing.T) {
	pInferred := vfPolicy(domain.AcceptAuto)
	r1 := pInferred.Admissions["github|comment"]
	r1.UnlistedAddressed = domain.CellInferred
	pInferred.Admissions["github|comment"] = r1
	pExplicit := vfPolicy(domain.AcceptAuto)

	a := &vfRecordingAssessor{out: AssessOutput{Kind: domain.DecIgnore}}
	e := New(a)
	e.Register(vfRepo("ws_a"), pInferred)
	e.Register(vfRepo("ws_b"), pExplicit)

	// A global Trigger: Scope global, no DefaultWorkspace.
	vfReceive(t, e, vfTrigger("global-policy", "stranger", true, true, true))
	if _, err := e.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if a.last.Policy.Version == "" {
		t.Fatalf("the judge saw an empty PolicyView for a global Trigger: %+v", a.last.Policy)
	}
	if len(a.last.Policy.Rules) == 0 {
		t.Fatalf("the judge saw no rules for a global Trigger: %+v", a.last.Policy)
	}
}

// Guard the "unobserved" fixtures: a case may not be named unobserved while its
// admission row still says Observed=true (that made the earlier bot case pass for
// the wrong reason — verifier-B's finding).
func TestVerifyUnobservedScopeFixture(t *testing.T) {
	for _, mode := range []domain.AcceptMode{domain.AcceptOff, domain.AcceptObserve, domain.AcceptAuto} {
		p := vfUnobservedPolicy(mode)
		if p.Admissions["github|comment"].Observed {
			t.Fatalf("mode=%s: the unobserved fixture row still has Observed=true", mode)
		}
		// A listed person's unaddressed message cannot use an unaddressed cell.
		human := vfTrigger("guard-human", "lin", true, false, false)
		if g := gate(p, human, false); g.Admitted {
			t.Fatalf("mode=%s: an unobserved scope admitted an unaddressed message: %+v", mode, g)
		}
		// Neither can a listed bot that does not address the agent.
		bot := vfTrigger("guard-bot", "dependabot[bot]", false, false, false)
		if g := gate(p, bot, false); g.Admitted {
			t.Fatalf("mode=%s: an unobserved scope admitted an unaddressed bot: %+v", mode, g)
		}
	}
}
