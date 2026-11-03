package view_test

// The IM channel catalog: which keys belong to which channel, and which
// transports this build can actually run.

import (
	"strings"
	"testing"

	. "github.com/zeokeel/super-agent/internal/view"
)

func TestChannelSpecs(t *testing.T) {
	specs := ChannelSpecs()
	if len(specs) != 2 {
		t.Fatalf("%d channel specs, want 2", len(specs))
	}
	want := map[string]string{"feishu": "飞书", "dingtalk": "钉钉"}
	for _, spec := range specs {
		if want[spec.Name] != spec.Label {
			t.Fatalf("channel %q label %q, want %q", spec.Name, spec.Label, want[spec.Name])
		}
		if spec.EnabledKey == "" || spec.TransportKey == "" {
			t.Fatalf("channel %q has no enable/transport key: %+v", spec.Name, spec)
		}
		if len(spec.CredentialKeys) == 0 {
			t.Fatalf("channel %q has no credential keys", spec.Name)
		}
		if len(spec.Options) == 0 {
			t.Fatalf("channel %q has no transports", spec.Name)
		}
		transports := map[string]ChannelOption{}
		for _, o := range spec.Options {
			if o.ID == "" || o.Label == "" {
				t.Fatalf("channel %q has an empty transport: %+v", spec.Name, o)
			}
			transports[o.ID] = o
		}
		switch spec.Name {
		case "feishu":
			if o, ok := transports["longpoll"]; !ok || !o.Implemented {
				t.Fatalf("feishu long-poll is not selectable: %+v", transports)
			}
		case "dingtalk":
			if o, ok := transports["dws"]; !ok || !o.Implemented {
				t.Fatalf("dingtalk DWS is not selectable: %+v", transports)
			}
			o, ok := transports["stream"]
			if !ok {
				t.Fatalf("dingtalk has no official-Stream option: %+v", transports)
			}
			if !o.Implemented {
				t.Fatal("official Stream is built by the runtime (runtime/im.go) but not selectable here")
			}
			if o.Note != "" && strings.Contains(o.Note, "未接入") {
				t.Fatalf("the option still says the Stream SDK is not wired: %q", o.Note)
			}
		}
	}
	if ChannelSpecOf("feishu") == nil || ChannelSpecOf("dingtalk") == nil {
		t.Fatal("ChannelSpecOf cannot find a channel")
	}
	if ChannelSpecOf("nope") != nil {
		t.Fatal("ChannelSpecOf invented a channel")
	}
}

// ChannelKey tells the settings page which 来源 fields the IM cards own.
func TestChannelKey(t *testing.T) {
	for _, key := range []string{
		"feishu_enabled", "feishu_transport", "feishu_app_id", "feishu_app_secret", "feishu_chats",
		"dingtalk_enabled", "dingtalk_transport", "dingtalk_app_key", "dingtalk_app_secret", "dingtalk_robot",
	} {
		if !ChannelKey(key) {
			t.Fatalf("%q is not recognised as a channel key", key)
		}
	}
	for _, key := range []string{"poll_interval", "im_self", "github_token", ""} {
		if ChannelKey(key) {
			t.Fatalf("%q was taken for a channel key", key)
		}
	}
}
