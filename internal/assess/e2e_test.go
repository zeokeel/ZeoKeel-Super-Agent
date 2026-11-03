package assess

import (
	"context"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
)

// The whole Eval path with a model in the loop: recall narrows the candidates, the
// assessor judges inside them, and the engine creates the Ticket the model named.
// This is the seam between M1 and M2, so it is tested against a real HTTP client.
func TestModelDrivenEvalCreatesTheTicketItChose(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	gen, err := NewEinoGenerator(context.Background(), EinoConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-assess",
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{Rules: defaultTestRules(), Model: "mock-assess"}, gen)
	if err != nil {
		t.Fatal(err)
	}

	e := engine.New(a)
	e.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "面向客户的 web 控制台", Keywords: []string{"webapp", "登录"}},
	}, engine.Policy{Version: "v1"})

	// Receiving is two-phase: the Trigger is queued, then EvaluateDue asks the
	// model outside the engine lock and applies its Decision (design §2.1).
	if _, err := e.Dispatch(engine.DispatchInput{Text: "在 webapp 里把登录超时改成可配置"}); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	outs, err := e.EvaluateDue(time.Now())
	if err != nil {
		t.Fatalf("the model-driven Eval failed: %v", err)
	}
	var ticketID domain.ID
	for _, out := range outs {
		if out.Ticket != "" {
			ticketID = out.Ticket
		}
	}
	if ticketID == "" {
		t.Fatalf("no accept produced a ticket: %+v", outs)
	}
	tk, ok := e.TicketSnapshot(ticketID)
	if !ok {
		t.Fatal("no ticket was created")
	}
	if tk.Workspace != "ws_web" || tk.Mandate.Strength != "explicit" {
		t.Fatalf("bad ticket: workspace=%s strength=%s", tk.Workspace, tk.Mandate.Strength)
	}
}

// defaultTestRules mirrors the app's rule ids, which the mock's reply names.
func defaultTestRules() []Rule {
	return []Rule{
		{ID: "implementation", Instruction: "要求修改仓库代码或配置", Enabled: true},
		{ID: "data-request", Instruction: "要求取数或出报表", Enabled: true},
		{ID: "off-topic", Instruction: "闲聊或通知", Enabled: true},
	}
}
