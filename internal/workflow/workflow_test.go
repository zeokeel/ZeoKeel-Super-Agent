package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func def() Definition {
	return Definition{Name: "ship", Steps: []Step{
		{ID: "plan", Title: "计划", Handler: "plan"},
		{ID: "build", Title: "构建", Handler: "build"},
		{ID: "verify", Title: "验证", Handler: "verify"},
	}}
}

func runner(t *testing.T) (*Runner, *MemoryStore) {
	t.Helper()
	st := NewMemoryStore()
	r, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	return r, st
}

func TestWorkflowRunsStepsInOrderAndRecordsOutputs(t *testing.T) {
	r, _ := runner(t)
	var order []string
	mk := func(name string) Handler {
		return func(_ context.Context, in HandlerInput) (string, error) {
			order = append(order, name)
			if name == "build" && in.Outputs["plan"] != "plan-out" {
				t.Errorf("build saw plan output %q", in.Outputs["plan"])
			}
			return name + "-out", nil
		}
	}
	for _, n := range []string{"plan", "build", "verify"} {
		if err := r.Register(n, mk(n)); err != nil {
			t.Fatal(err)
		}
	}
	run, err := r.Start(context.Background(), def(), map[string]string{"task": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusDone {
		t.Fatalf("status %q", run.Status)
	}
	if strings.Join(order, ",") != "plan,build,verify" {
		t.Fatalf("order %v", order)
	}
	if run.Outputs["verify"] != "verify-out" {
		t.Fatalf("outputs %+v", run.Outputs)
	}
	for id, st := range run.StepStatus {
		if st != "done" {
			t.Fatalf("step %s is %s", id, st)
		}
	}
}

// A blocked step keeps its place: the run says blocked, and nothing after it ran.
func TestWorkflowBlockedStopsTheChain(t *testing.T) {
	r, _ := runner(t)
	_ = r.Register("plan", func(context.Context, HandlerInput) (string, error) { return "planned", nil })
	_ = r.Register("build", func(context.Context, HandlerInput) (string, error) {
		return "", ErrBlocked
	})
	verifyRan := false
	_ = r.Register("verify", func(context.Context, HandlerInput) (string, error) {
		verifyRan = true
		return "", nil
	})
	run, err := r.Start(context.Background(), def(), nil)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected blocked, got %v", err)
	}
	if run.Status != StatusBlocked || run.StepStatus["build"] != "pending" {
		t.Fatalf("run %+v", run)
	}
	if verifyRan {
		t.Fatal("a blocked step must stop the chain")
	}
}

func TestDefinitionValidation(t *testing.T) {
	cases := map[string]Definition{
		"no name":  {Steps: []Step{{ID: "a"}}},
		"no steps": {Name: "x"},
		"dup id":   {Name: "x", Steps: []Step{{ID: "a"}, {ID: "a"}}},
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			if err := d.Validate(); err == nil {
				t.Fatal("expected a refusal")
			}
		})
	}
}

func TestUnknownHandlerFailsTheRun(t *testing.T) {
	r, _ := runner(t)
	_, err := r.Start(context.Background(), def(), nil)
	if err == nil || !strings.Contains(err.Error(), "no handler") {
		t.Fatalf("got %v", err)
	}
}

func TestRegisterRejectsDuplicates(t *testing.T) {
	r, _ := runner(t)
	if err := r.Register("a", func(context.Context, HandlerInput) (string, error) { return "", nil }); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("a", func(context.Context, HandlerInput) (string, error) { return "", nil }); err == nil {
		t.Fatal("a duplicate handler must be refused")
	}
}
