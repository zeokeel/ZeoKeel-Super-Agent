package github

import (
	"testing"

	"github.com/zeokeel/super-agent/internal/sources"
)

func TestKindIsRegisteredAtCompileTime(t *testing.T) {
	factory, ok := sources.Lookup(Name)
	if !ok {
		t.Fatalf("%q was not registered by the package's init()", Name)
	}
	kind, err := factory([]byte(`{"repository":"acme/webapp","login":"zeokeel-agent"}`))
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if kind.Name() != Name {
		t.Fatalf("Name() = %q, want %q", kind.Name(), Name)
	}
	// Runtime 靠 type assertion 发现可选能力；这里把三类都钉住。
	if _, ok := kind.(sources.Poller); !ok {
		t.Fatal("GitHub Source does not implement sources.Poller")
	}
	if _, ok := kind.(sources.MemberLister); !ok {
		t.Fatal("GitHub Source does not implement sources.MemberLister")
	}
	if _, ok := kind.(sources.HealthReporter); !ok {
		t.Fatal("GitHub Source does not implement sources.HealthReporter")
	}
}

func TestFactoryRejectsUnreadableConfig(t *testing.T) {
	factory, ok := sources.Lookup(Name)
	if !ok {
		t.Fatalf("%q was not registered", Name)
	}
	if _, err := factory([]byte(`{`)); err == nil {
		t.Fatal("factory accepted invalid config JSON")
	}
	// 空配置能解码，但 New 的校验会拒绝：缺仓库/账号是配置错误，不是 panic。
	if _, err := factory(nil); err == nil {
		t.Fatal("factory accepted a config with no repository or login")
	}
}

func TestNewValidatesConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"missing repository", Config{Login: "zeokeel-agent"}},
		{"malformed repository", Config{Repository: "not-a-repo", Login: "zeokeel-agent"}},
		{"missing login", Config{Repository: "acme/webapp"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg, &fakeCommand{}); err == nil {
				t.Fatal("New accepted an invalid config")
			}
		})
	}
	if _, err := New(Config{Repository: "acme/webapp", Login: "zeokeel-agent"}, nil); err == nil {
		t.Fatal("New accepted a nil Command")
	}
}

func TestNewNormalizesConfig(t *testing.T) {
	src, err := New(Config{
		Repository: " acme/webapp ",
		Login:      "@zeokeel-agent",
		Self:       []string{" @zeokeel-operator "},
	}, &fakeCommand{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if src.cfg.Repository != "acme/webapp" {
		t.Errorf("Repository = %q", src.cfg.Repository)
	}
	if src.cfg.Login != "zeokeel-agent" {
		t.Errorf("Login = %q", src.cfg.Login)
	}
	if src.mention() != "zeokeel-agent" {
		t.Errorf("Mention default = %q, want Login", src.mention())
	}
	if !src.isSelf("ZEOKEEL-OPERATOR") || !src.isSelf("zeokeel-operator") {
		t.Errorf("Self normalization lost the operator account: %v", src.cfg.Self)
	}
}

func TestNewKeepsIndependentMentionHandle(t *testing.T) {
	src, err := New(Config{
		Repository: "acme/webapp",
		Login:      "zeokeel-agent",
		Mention:    "zeokeel",
	}, &fakeCommand{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if src.mention() != "zeokeel" {
		t.Fatalf("Mention = %q, want the configured handle", src.mention())
	}
}
