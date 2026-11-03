package dingtalk

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
)

// translate 把一条钉钉 Stream 回调翻译成 RawEvent。只翻译事实，不做准入：
// 群聊里"没 @ 机器人"的消息如果真的被 Stream 推进来了（配置了接收全部），
// 是否该成为候选由 Source/Gate 决定，驱动不替它决定。
func (d *Driver) translate(e Event, selfID string) (im.RawEvent, error) {
	if e.MsgID == "" {
		return im.RawEvent{}, errors.New("dingtalk: event without msg id")
	}
	if e.ConversationID == "" {
		return im.RawEvent{}, errors.New("dingtalk: event without conversation id")
	}
	actorID := e.SenderStaffID
	if actorID == "" {
		actorID = e.SenderID
	}
	if actorID == "" {
		return im.RawEvent{}, errors.New("dingtalk: event without sender")
	}
	if e.CreateAt == 0 {
		return im.RawEvent{}, errors.New("dingtalk: event without create time")
	}
	kind := conversationKind(e.ConversationType)
	ev := im.RawEvent{
		ID:   e.MsgID,
		Conv: domain.Conversation{Kind: kind, ID: e.ConversationID, Name: e.ConversationTitle},
		Actor: domain.Actor{
			ID: actorID,
			// 来源标记优先用传输层给的事实（DWS 的 sender.bot；Stream 回调
			// 没有 bot 标志）。没有事实时退回启发式：钉钉企业内的人的
			// senderStaffId 一定有值，机器人/外部身份没有 staffId。
			Bot: e.SenderIsBot || e.SenderStaffID == "",
		},
		// Stream 回调只告诉"这是一条回复"（isReplyMsg），不给父消息 id；
		// 因此 ReplyTo 留空，只把"回复了机器人"这个事实交给 Addressed。
		ReplyTo:    "",
		ReplyToBot: e.IsReplyMsg,
		At:         e.CreateAt,
	}
	content, err := renderContent(e.MsgType, e.Content)
	if err != nil {
		// 贴纸/视频等不认识的类型：保留事件用于计数，但不给它正文，
		// 由 Source 决定不建 Ticket；游标照常推进，避免一条坏消息卡住会话。
		ev.Unsupported = true
		return ev, nil
	}
	ev.Text = stripAddressedPrefix(content.Text, d.cfg.BotName)
	ev.Mentions = resolveMentions(e, selfID, d.cfg.BotName)
	ev.MediaRef = content.Media
	return ev, nil
}

func conversationKind(conversationType string) domain.ConversationKind {
	switch conversationType {
	case "1":
		return domain.ConvDirect
	case "2":
		return domain.ConvGroup
	}
	// 钉钉只有单聊/群聊两种；缺省按群处理，避免把陌生人私聊误当"直接对
	// agent 说"而放大权限（Direct 会绕过 @ 检查）。
	return domain.ConvGroup
}

// resolveMentions 解析本条消息 @ 了谁。
//
// 三条边界必须同时成立，否则"@所有人"会被误判成"@ 了 agent"：
//  1. atUsers 里的 @所有人 用 "@all"/空 id 表示，直接剔除；
//  2. 只保留真实用户 id（dingtalkId 优先，staffId 兜底），空 id 不算；
//  3. 少数回调不带 atUsers 但 isInAtList=true 时，用"正文里是否有 @机器人名"
//     兜底——@所有人的正文是 @所有人，不会等于机器人名，所以不会误判。
func resolveMentions(e Event, selfID, botName string) []string {
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if id == "" || strings.EqualFold(id, "all") || strings.EqualFold(id, "@all") || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, user := range e.AtUsers {
		id := user.DingTalkID
		if id == "" {
			id = user.StaffID
		}
		add(id)
	}
	if len(ids) == 0 && e.IsInAtList && selfID != "" && botName != "" {
		// 兜底：正文里出现 "@机器人名" 才算 @ 了机器人。
		if strings.Contains(textOf(e.MsgType, e.Content), "@"+botName) {
			add(selfID)
		}
	}
	return ids
}

// textOf 尽力取出正文（只用于 @ 判定兜底，不追求渲染完整）。
func textOf(msgType, content string) string {
	rendered, err := renderContent(msgType, content)
	if err != nil {
		return ""
	}
	return rendered.Text
}

// stripAddressedPrefix 只去掉正文里的 @机器人名（它只是协议前缀，留着会让
// 模型重复看到机器人名字）。@所有人 保留：它是"广播"这个事实的可读表达，
// 不是对 agent 说话；驱动层已经保证它不进 Mentions。
func stripAddressedPrefix(text, botName string) string {
	if botName != "" {
		text = strings.ReplaceAll(text, "@"+botName, "")
	}
	return im.CollapseSpaces(text)
}

type renderedContent struct {
	Text  string
	Media []string
}

// renderContent 按消息类型解析 content JSON。
func renderContent(msgType, raw string) (renderedContent, error) {
	switch msgType {
	case "text":
		var body struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			return renderedContent{}, fmt.Errorf("dingtalk: bad text body: %w", err)
		}
		return renderedContent{Text: body.Content}, nil
	case "markdown":
		var body struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			return renderedContent{}, fmt.Errorf("dingtalk: bad markdown body: %w", err)
		}
		text := body.Text
		if strings.TrimSpace(body.Title) != "" {
			text = body.Title + "\n" + text
		}
		return renderedContent{Text: text}, nil
	case "richText":
		var body struct {
			Content []struct {
				Text         string `json:"text"`
				DownloadCode string `json:"downloadCode"`
			} `json:"content"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			return renderedContent{}, fmt.Errorf("dingtalk: bad richText body: %w", err)
		}
		var (
			lines []string
			media []string
		)
		for _, item := range body.Content {
			if item.Text != "" {
				lines = append(lines, item.Text)
			}
			if item.DownloadCode != "" {
				media = append(media, item.DownloadCode)
			}
		}
		return renderedContent{Text: strings.Join(lines, "\n"), Media: media}, nil
	case "picture":
		var body struct {
			DownloadCode string `json:"downloadCode"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil || body.DownloadCode == "" {
			return renderedContent{}, errors.New("dingtalk: bad picture body")
		}
		return renderedContent{Text: "[图片]", Media: []string{body.DownloadCode}}, nil
	case "file":
		var body struct {
			DownloadCode string `json:"downloadCode"`
			FileName     string `json:"fileName"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil || body.DownloadCode == "" {
			return renderedContent{}, errors.New("dingtalk: bad file body")
		}
		return renderedContent{Text: "[文件] " + body.FileName, Media: []string{body.DownloadCode}}, nil
	case "audio":
		var body struct {
			DownloadCode string `json:"downloadCode"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil || body.DownloadCode == "" {
			return renderedContent{}, errors.New("dingtalk: bad audio body")
		}
		return renderedContent{Text: "[语音]", Media: []string{body.DownloadCode}}, nil
	case "video":
		return renderedContent{Text: "[视频]"}, nil
	default:
		return renderedContent{}, fmt.Errorf("dingtalk: unsupported message type %q", msgType)
	}
}
