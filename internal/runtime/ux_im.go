package runtime

// UI-facing IM adapters (UX U-03/A18): primitive-typed wrappers around the IM
// manager's richer API, so the UI can define optional interfaces without
// importing package runtime.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// imManager returns the attached IM manager, or nil.
func (r *Runtime) imManager() *IMManager {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.im
}

// ValidateChannelDraft validates unsaved channel settings and returns one line
// per problem (empty means valid).
func (r *Runtime) ValidateChannelDraft(name string, values map[string]string) []string {
	issues := ValidateChannelConfig(name, values)
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.Field == "" {
			out = append(out, issue.Message)
			continue
		}
		out = append(out, issue.Field+"："+issue.Message)
	}
	return out
}

// DWSVersionValue reports the local `dws` version (for the settings card).
func (r *Runtime) DWSVersionValue() (string, error) {
	m := r.imManager()
	if m == nil {
		return "", fmt.Errorf("dws 未接线（没有 IM 管理器）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return m.DWSVersion(ctx)
}

// DWSLoginArgs is the command a person should run to authorize DWS (not executed
// by the runtime).
func (r *Runtime) DWSLoginArgs() []string {
	if m := r.imManager(); m != nil {
		if args := m.DWSLoginCommand(); len(args) > 0 {
			return args
		}
	}
	return []string{"dws", "auth", "login"}
}

// IMConnectionState is the primitive view of one channel's connection: the UI can
// render "已连接/需要登录/配置错误" without importing runtime types.
func (r *Runtime) IMConnectionState(name string) (state, detail, nextAction, authURL, login, version string, ok bool) {
	conn, found := r.IMConnection(name)
	if !found {
		return "", "未配置", "", "", "", "", false
	}
	login = strings.Join(conn.LoginCommand, " ")
	return conn.State, conn.Detail, conn.NextAction, conn.AuthURL, login, conn.Version, true
}

// ChannelFieldMetadata is the transport/field model in primitive form: one line
// per field, "channel|transport|key|kind|required|default|options". The panel can
// parse it without importing runtime types; the full typed list stays available
// to Go callers via IMChannelConfigSpecs.
func (r *Runtime) ChannelFieldMetadata() []string {
	var out []string
	for _, spec := range IMChannelConfigSpecs() {
		for _, tr := range spec.Transports {
			impl := "1"
			if !tr.Implemented {
				impl = "0"
			}
			for _, f := range tr.Fields {
				req := "0"
				if f.Required {
					req = "1"
				}
				out = append(out, strings.Join([]string{
					spec.Name, tr.ID, impl, f.Key, f.Kind, req, f.Default,
					strings.Join(f.Options, ","), f.Label, f.Help,
				}, "|"))
			}
		}
	}
	return out
}
