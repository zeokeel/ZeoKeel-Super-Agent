// Command seedstate prepares a state directory for demos and screenshots: one
// workspace, optionally a saved model. It is a development tool, not part of the
// shipped app, and it exists because poking the wizard with a mouse is a poor way
// to make a repeatable screenshot.
//
//	go run ./tools/seedstate -dir /tmp/zk-live -name webapp -root /tmp/webapp
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/effects"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/outbox"
	"github.com/zeokeel/super-agent/internal/store"
)

func main() {
	dir := flag.String("dir", "", "state directory (created if missing)")
	name := flag.String("name", "webapp", "workspace name")
	root := flag.String("root", "", "workspace root path")
	purpose := flag.String("purpose", "面向客户的 web 控制台：登录、账单、设置", "one line about the workspace")
	keywords := flag.String("keywords", "webapp,登录,账单", "comma separated recall keywords")
	model := flag.String("model", "", "model id to save as the default")
	baseURL := flag.String("base-url", "", "endpoint to save as the default")
	apiKey := flag.String("api-key", "", "key to save (kept in the local database)")
	allow := flag.String("allow", "", "allowlist entry, source:actor")
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "seedstate: -dir is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		fail(err)
	}
	if *root == "" {
		*root = filepath.Join(*dir, "ws", *name)
	}
	if err := os.MkdirAll(*root, 0o755); err != nil {
		fail(err)
	}

	st, err := store.Open(filepath.Join(*dir, "zeokeel.db"))
	if err != nil {
		fail(err)
	}
	defer st.Close()
	q, err := effects.New(st)
	if err != nil {
		fail(err)
	}
	box, err := outbox.Open(st, q, noopSender{})
	if err != nil {
		fail(err)
	}
	eng := engine.New(engine.RuleAssessor{})
	pers, err := engine.NewPersistence(eng, st, q, box)
	if err != nil {
		fail(err)
	}

	policy := engine.Policy{Version: "v1"}
	if *allow != "" {
		source, actor, err := parseAllow(*allow)
		if err != nil {
			fail(err)
		}
		policy.Allowed = map[string]map[string]bool{source: {actor: true}}
	}
	if err := pers.Register(domain.Workspace{
		ID:    domain.WorkspaceID("ws_" + *name),
		Name:  *name,
		Root:  *root,
		Kind:  domain.KindRepo,
		Brief: domain.Brief{Purpose: *purpose, Keywords: splitCSV(*keywords), Facts: domain.Facts{ObservedAt: time.Now()}},
	}, policy); err != nil {
		fail(err)
	}

	settings := map[string]string{"onboard.ws_name": *name, "onboard.ws_root": *root,
		"onboard.ws_purpose": *purpose, "onboard.ws_keywords": *keywords, "onboard.done": "1"}
	if *model != "" {
		settings["onboard.model"] = *model
		settings["onboard.base_url"] = *baseURL
		settings["onboard.api_key"] = *apiKey
	}
	if _, err := st.SaveSettings(settings, 0, false); err != nil {
		fail(err)
	}
	fmt.Printf("seeded %s: workspace %s at %s\n", *dir, *name, *root)
}

type noopSender struct{}

func (noopSender) Send(context.Context, outbox.Message) (outbox.SendOutcome, error) {
	return outbox.OutcomeSent, nil
}

// parseAllow 把 `source:actor` 拆成两段。用 strings.Cut 而不是
// fmt.Sscanf("%[^:]:%s")：Go 的 fmt 不支持 scanset，后者必然报
// "bad verb '%['"，-allow 参数从来没能生效过（audit §6 / P2）。
func parseAllow(value string) (source, actor string, err error) {
	source, actor, ok := strings.Cut(strings.TrimSpace(value), ":")
	source, actor = strings.TrimSpace(source), strings.TrimSpace(actor)
	if !ok || source == "" || actor == "" {
		return "", "", fmt.Errorf("seedstate: -allow must be source:actor, got %q", value)
	}
	return source, actor, nil
}

// splitCSV 是 strings.Split 的薄封装：保留"跳过空项"的播种语义。
func splitCSV(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "seedstate:", err)
	os.Exit(1)
}
