package sources

import (
	"context"
	"errors"
	"testing"

	"github.com/zeokeel/super-agent/internal/domain"
)

// fakeKind 是"写一个 Source 有多容易"的证明：四个方法、零网络、零时钟。
type fakeKind struct {
	name  Name
	scope ScopeResult
	tr    domain.Trigger
}

func (f fakeKind) Name() Name                            { return f.name }
func (f fakeKind) Scope(domain.Trigger) ScopeResult      { return f.scope }
func (f fakeKind) Translate(Raw) (domain.Trigger, error) { return f.tr, nil }
func (f fakeKind) Channel() Channel                      { return fakeChannel{} }

// fakeChannel 记录投递并原样回一个回执。测试 Channel 契约不需要 gh。
type fakeChannel struct{ sent *[]Outbound }

func (c fakeChannel) Send(_ context.Context, msg Outbound) (Receipt, error) {
	if c.sent != nil {
		*c.sent = append(*c.sent, msg)
	}
	return Receipt{MessageID: msg.Key}, nil
}

// 编译期断言：接口足够小，fake 不需要任何技巧就能满足。
var (
	_ Kind    = fakeKind{}
	_ Channel = fakeChannel{}
)

func TestFakeKindIsEnoughForTheContract(t *testing.T) {
	want := domain.Trigger{ID: "t1", Source: "fake"}
	var sent []Outbound
	k := fakeKind{name: "fake", scope: ScopeResult{Listened: true, Row: "comments"}, tr: want}

	if got := k.Name(); got != "fake" {
		t.Fatalf("Name() = %q, want fake", got)
	}
	tr, err := k.Translate(Raw{Kind: "x", Data: []byte("{}")})
	if err != nil || tr.ID != want.ID {
		t.Fatalf("Translate() = %+v, %v", tr, err)
	}
	if got := k.Scope(want); !got.Listened || got.Row != "comments" {
		t.Fatalf("Scope() = %+v", got)
	}
	receipt, err := fakeChannel{sent: &sent}.Send(context.Background(), Outbound{Key: "k1", Text: "hi"})
	if err != nil || receipt.MessageID != "k1" || len(sent) != 1 {
		t.Fatalf("Send() = %+v, %v, sent=%v", receipt, err, sent)
	}
}

func TestRegistryRegisterLookupNames(t *testing.T) {
	r := newRegistry()
	if err := r.register("b", func([]byte) (Kind, error) { return fakeKind{name: "b"}, nil }); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if err := r.register("a", func([]byte) (Kind, error) { return fakeKind{name: "a"}, nil }); err != nil {
		t.Fatalf("register a: %v", err)
	}

	names := r.names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("names() = %v, want sorted [a b]", names)
	}

	f, ok := r.lookup("a")
	if !ok {
		t.Fatal("lookup(a) not found")
	}
	kind, err := f(nil)
	if err != nil || kind.Name() != "a" {
		t.Fatalf("factory = %v, %v", kind, err)
	}

	if _, ok := r.lookup("missing"); ok {
		t.Fatal("lookup(missing) unexpectedly found")
	}
}

func TestRegistryRejectsEmptyAndDuplicateNames(t *testing.T) {
	r := newRegistry()
	f := func([]byte) (Kind, error) { return fakeKind{}, nil }

	if err := r.register("", f); err == nil {
		t.Fatal("empty name was accepted")
	}
	if err := r.register("dup", nil); err == nil {
		t.Fatal("nil factory was accepted")
	}
	if err := r.register("dup", f); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.register("dup", f); err == nil {
		t.Fatal("duplicate name was accepted")
	}
}

func TestRegisterPanicsOnDuplicate(t *testing.T) {
	name := Name("test-register-panic")
	Register(name, func([]byte) (Kind, error) { return fakeKind{name: name}, nil })

	defer func() {
		if recover() == nil {
			t.Fatal("Register did not panic on a duplicate kind")
		}
	}()
	Register(name, func([]byte) (Kind, error) { return fakeKind{name: name}, nil })
}

func TestNewBuildsFromRegisteredFactory(t *testing.T) {
	name := Name("test-new")
	Register(name, func(config []byte) (Kind, error) {
		if string(config) != "cfg" {
			return nil, errors.New("bad config")
		}
		return fakeKind{name: name}, nil
	})

	kind, err := New(name, []byte("cfg"))
	if err != nil || kind.Name() != name {
		t.Fatalf("New() = %v, %v", kind, err)
	}
	if _, err := New(name, []byte("other")); err == nil {
		t.Fatal("New did not surface the factory's config error")
	}
	if _, err := New("never-registered", nil); err == nil {
		t.Fatal("New accepted an unknown kind")
	}
}
