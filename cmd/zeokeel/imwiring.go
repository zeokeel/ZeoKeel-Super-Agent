package main

// IM wiring: Feishu/DingTalk are opt-in. A build with no credentials must not
// pretend to listen (no "online" source row, no health "ready"); a half-configured
// driver is an error row naming the missing keys, not a silent skip.

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/im/dingtalk"
	"github.com/zeokeel/super-agent/internal/im/feishu"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/view"
)

// imOptions is the deployment's IM configuration: flags first, saved settings as
// fallback (a value edited in the panel survives a restart).
type imOptions struct {
	FeishuAppID     string
	FeishuAppSecret string
	FeishuChats     string
	DingAppKey      string
	DingAppSecret   string
	DingRobot       string
	SelfID          string
}

// savedIM fills empty fields from the store's settings (group "source").
func (o imOptions) saved(kv map[string]string) imOptions {
	get := func(flag, key string) string {
		if strings.TrimSpace(flag) != "" {
			return strings.TrimSpace(flag)
		}
		return strings.TrimSpace(kv["source."+key])
	}
	return imOptions{
		FeishuAppID:     get(o.FeishuAppID, "feishu_app_id"),
		FeishuAppSecret: get(o.FeishuAppSecret, "feishu_app_secret"),
		FeishuChats:     get(o.FeishuChats, "feishu_chats"),
		DingAppKey:      get(o.DingAppKey, "dingtalk_app_key"),
		DingAppSecret:   get(o.DingAppSecret, "dingtalk_app_secret"),
		DingRobot:       get(o.DingRobot, "dingtalk_robot"),
		SelfID:          get(o.SelfID, "im_self"),
	}
}

func (o imOptions) feishuConfigured() bool {
	return o.FeishuAppID != "" || o.FeishuAppSecret != "" || o.FeishuChats != ""
}
func (o imOptions) dingtalkConfigured() bool {
	return o.DingAppKey != "" || o.DingAppSecret != "" || o.DingRobot != ""
}

// startConfiguredIM builds and starts the drivers the deployment configured.
// Errors are source rows (visible on 来源 and in health), never fatal: one bad
// IM config must not keep the window from opening.
func startConfiguredIM(ctx context.Context, rt *runtime.Runtime, sender *sourceSender, o imOptions, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if o.feishuConfigured() {
		if missing := missingKeys("飞书", map[string]string{
			"app id": o.FeishuAppID, "app secret": o.FeishuAppSecret,
		}); missing != "" {
			reportIMConfigError(rt, "feishu", "飞书", missing)
		} else if drv, err := feishu.New(feishu.Config{
			AppID: o.FeishuAppID, AppSecret: o.FeishuAppSecret,
			Chats: parseFeishuChats(o.FeishuChats), SelfID: o.SelfID, SelfName: o.SelfID,
		}); err != nil {
			reportIMConfigError(rt, "feishu", "飞书", err.Error())
		} else {
			wireIM(ctx, rt, sender, "feishu", "飞书", drv, o.SelfID, logger)
		}
	}
	if o.dingtalkConfigured() {
		if missing := missingKeys("钉钉", map[string]string{
			"app key": o.DingAppKey, "app secret": o.DingAppSecret, "robot code": o.DingRobot,
		}); missing != "" {
			reportIMConfigError(rt, "dingtalk", "钉钉", missing)
		} else if drv, err := dingtalk.New(dingtalk.Config{
			AppKey: o.DingAppKey, AppSecret: o.DingAppSecret, RobotCode: o.DingRobot, BotName: "ZeoKeel",
		}); err != nil {
			reportIMConfigError(rt, "dingtalk", "钉钉", err.Error())
		} else {
			wireIM(ctx, rt, sender, "dingtalk", "钉钉", drv, o.SelfID, logger)
		}
	}
	if !o.feishuConfigured() && !o.dingtalkConfigured() {
		logger.Info("IM not configured; no listener started", "feishu", "off", "dingtalk", "off")
	}
}

// missingKeys names the configuration a partially filled driver still needs.
func missingKeys(label string, fields map[string]string) string {
	var missing []string
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return label + " 配置不完整：缺少 " + strings.Join(missing, "、")
}

func reportIMConfigError(rt *runtime.Runtime, kind, label, problem string) {
	rt.SetSourceStatus(runtime.SourceStatus{
		Kind: kind, Name: label, Enabled: false,
		Last: view.PollResult{At: time.Now(), Outcome: view.PollFailed, Problem: problem, Detail: "配置错误"},
	})
	rt.SetComponentHealth("im:"+kind, "error", problem)
}

// wireIM starts one driver: its sender goes into the outbox router, its source
// row and health row say what it is doing.
func wireIM(ctx context.Context, rt *runtime.Runtime, sender *sourceSender, kind, label string,
	drv im.Provider, selfID string, logger *slog.Logger) {
	if sender != nil {
		if s, ok := drv.(im.Sender); ok {
			sender.setIM(kind, s)
		}
	}
	rt.SetSourceStatus(runtime.SourceStatus{
		Kind: kind, Name: label, Enabled: true,
		Facts: []view.Fact{{Label: "驱动", Value: label}},
		Last:  view.PollResult{At: time.Now(), Outcome: view.PollOK, Detail: "已配置，正在连接"},
	})
	rt.SetComponentHealth("im:"+kind, "starting", "连接中")
	// The loop blocks for the life of the process; the app boot must not.
	go startIMLoop(ctx, rt, drv, label, selfID, logger)
}

// parseFeishuChats turns "oc_1,oc_2" into the chat list the driver listens to.
func parseFeishuChats(raw string) []feishu.Chat {
	var out []feishu.Chat
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, feishu.Chat{ID: id})
		}
	}
	return out
}
