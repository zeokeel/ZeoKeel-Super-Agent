package feishu

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
)

// translate 把一条飞书消息拍成 RawEvent。这里只做"事实翻译"，不做任何准入
// 判断：是否该收、是否 @ 了 agent，由 Source 与 Gate 决定（§4.8/§4.9）。
func (d *Driver) translate(m Message, chat Chat, selfID string) (im.RawEvent, error) {
	if m.MessageID == "" {
		return im.RawEvent{}, errors.New("feishu: message without id")
	}
	chatID := m.ChatID
	if chatID == "" {
		chatID = chat.ID
	}
	if chatID == "" {
		return im.RawEvent{}, errors.New("feishu: message without chat id")
	}
	kind := conversationKind(m.ChatType, chat.Kind)
	conv := domain.Conversation{Kind: kind, ID: chatID, Name: chat.Name}
	if kind == domain.ConvTopic {
		// 只有话题（topic）才把 root 带进 Thread 身份；普通群里回复别人的
		// root_id 不该把一条消息拆成新 Thread。
		conv.RootID = m.RootID
	}
	if m.SenderID == "" {
		return im.RawEvent{}, errors.New("feishu: message without sender")
	}
	ev := im.RawEvent{
		ID:      m.MessageID,
		Conv:    conv,
		Actor:   domain.Actor{ID: m.SenderID, Bot: m.SenderType != "" && m.SenderType != "user"},
		ReplyTo: m.ParentID,
		// 只有确证父消息是自己发的，才算"回复 agent"这个事实；否则只是
		// 回复别人。缓存里没有的消息（重启前发的）保守判 false。
		ReplyToBot: d.isOwn(m.ParentID),
		At:         m.CreateMillis,
	}
	if ev.At == 0 {
		return im.RawEvent{}, errors.New("feishu: message without create time")
	}
	content, err := renderContent(m.MsgType, m.Content, m.Mentions, selfID)
	if err != nil {
		// 不认识的类型不整批失败：标成 Unsupported 让面板能解释，
		// 同时仍推进游标（否则一条贴纸会卡住整个会话）。
		ev.Unsupported = true
		ev.Text = ""
		return ev, nil
	}
	ev.Text = content.Text
	ev.Mentions = content.Mentions
	ev.MediaRef = content.Media
	return ev, nil
}

func conversationKind(chatType string, fallback domain.ConversationKind) domain.ConversationKind {
	switch strings.ToLower(chatType) {
	case "p2p":
		return domain.ConvDirect
	case "topic":
		return domain.ConvTopic
	case "group":
		return domain.ConvGroup
	}
	if fallback != "" {
		return fallback
	}
	return domain.ConvGroup
}

// rendered 是一段可读文本 + 它 @ 了谁 + 带了哪些媒体引用。
type rendered struct {
	Text     string
	Mentions []string
	Media    []string
}

// renderContent 按消息类型解析 content JSON。飞书的 content 是"JSON 字符串"
// 而不是嵌套对象，这是它最容易踩的坑之一，所以解析集中在这里。
func renderContent(msgType, raw string, mentions []Mention, selfID string) (rendered, error) {
	switch msgType {
	case "text":
		var body struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			return rendered{}, fmt.Errorf("feishu: bad text body: %w", err)
		}
		return rendered{Text: replaceMentionKeys(body.Text, mentions, selfID), Mentions: mentionIDs(mentions)}, nil
	case "post":
		return renderPost(raw, mentions, selfID)
	case "image":
		var body struct {
			ImageKey string `json:"image_key"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil || body.ImageKey == "" {
			return rendered{}, errors.New("feishu: bad image body")
		}
		return rendered{Text: "[图片]", Mentions: mentionIDs(mentions), Media: []string{body.ImageKey}}, nil
	case "file":
		var body struct {
			FileKey  string `json:"file_key"`
			FileName string `json:"file_name"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil || body.FileKey == "" {
			return rendered{}, errors.New("feishu: bad file body")
		}
		return rendered{Text: "[文件] " + body.FileName, Mentions: mentionIDs(mentions), Media: []string{body.FileKey}}, nil
	case "media":
		var body struct {
			FileKey string `json:"file_key"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil || body.FileKey == "" {
			return rendered{}, errors.New("feishu: bad media body")
		}
		return rendered{Text: "[视频]", Mentions: mentionIDs(mentions), Media: []string{body.FileKey}}, nil
	case "audio":
		var body struct {
			FileKey string `json:"file_key"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil || body.FileKey == "" {
			return rendered{}, errors.New("feishu: bad audio body")
		}
		return rendered{Text: "[语音]", Mentions: mentionIDs(mentions), Media: []string{body.FileKey}}, nil
	default:
		return rendered{}, fmt.Errorf("feishu: unsupported message type %q", msgType)
	}
}

// mentionIDs 收集"真实的人"的 id：@所有人 的 open_id 为空，必须剔除——
// 这正是 @ 判定边界里最容易错的一条（@all 不是 @ 了 agent）。
func mentionIDs(mentions []Mention) []string {
	var ids []string
	seen := map[string]bool{}
	for _, mention := range mentions {
		id := mention.OpenID
		if id == "" || strings.EqualFold(id, "all") || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// replaceMentionKeys 把正文里的 @_user_N 占位符换成可读名字；@ 到自己时整段
// 去掉（模型不需要看到"@我"的协议占位符，正文语义已由 Addressed.Mention 表达）。
func replaceMentionKeys(text string, mentions []Mention, selfID string) string {
	out := text
	for _, mention := range mentions {
		if mention.Key == "" {
			continue
		}
		replacement := ""
		if mention.OpenID == "" || mention.OpenID == selfID {
			// @所有人 保留"@名字"让人看懂；@自己 直接去掉。
			if mention.OpenID == "" && mention.Name != "" {
				replacement = "@" + mention.Name
			}
		} else if mention.Name != "" {
			replacement = "@" + mention.Name
		}
		out = strings.ReplaceAll(out, mention.Key, replacement)
	}
	return im.CollapseSpaces(out)
}

// postBlock 是飞书富文本（post）里的一个元素。
type postBlock struct {
	Tag      string `json:"tag"`
	Text     string `json:"text"`
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	ImageKey string `json:"image_key"`
	FileKey  string `json:"file_key"`
	Href     string `json:"href"`
}

// renderPost 把富文本拍成"按行拼接的纯文本 + 提及 + 媒体"。飞书 post 是
// 二维数组（行 × 元素），丢掉结构但保留行，模型仍能读懂；@ 的 id 同时补进
// Mentions，保证 Addressed 判定不丢。
func renderPost(raw string, mentions []Mention, selfID string) (rendered, error) {
	var body struct {
		Title   string        `json:"title"`
		Content [][]postBlock `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return rendered{}, fmt.Errorf("feishu: bad post body: %w", err)
	}
	out := rendered{Mentions: mentionIDs(mentions)}
	seen := map[string]bool{}
	for _, id := range out.Mentions {
		seen[id] = true
	}
	var lines []string
	if strings.TrimSpace(body.Title) != "" {
		lines = append(lines, strings.TrimSpace(body.Title))
	}
	for _, row := range body.Content {
		var b strings.Builder
		for _, block := range row {
			switch block.Tag {
			case "text":
				b.WriteString(block.Text)
			case "at":
				if block.UserID != "" && !strings.EqualFold(block.UserID, "all") && !seen[block.UserID] {
					seen[block.UserID] = true
					out.Mentions = append(out.Mentions, block.UserID)
				}
				if block.UserName != "" && block.UserID != selfID {
					b.WriteString("@" + block.UserName)
				}
			case "a":
				b.WriteString(block.Text)
				if block.Href != "" {
					b.WriteString(" " + block.Href)
				}
			case "img":
				if block.ImageKey != "" {
					out.Media = append(out.Media, block.ImageKey)
				}
			case "media":
				if block.FileKey != "" {
					out.Media = append(out.Media, block.FileKey)
				}
			}
		}
		if line := strings.TrimSpace(b.String()); line != "" {
			lines = append(lines, line)
		}
	}
	out.Text = im.CollapseSpaces(strings.Join(lines, "\n"))
	return out, nil
}

// textBody 生成飞书 text 消息的 content。
func textBody(text string) string {
	encoded, _ := json.Marshal(map[string]string{"text": text})
	return string(encoded)
}

// markdownCard 把 markdown 包成最小 interactive 卡片：飞书 text 消息不渲染
// markdown，直接发会显示成一堆 ** 和 |；Capabilities 声明了 Markdown 就必须
// 真的渲染成卡片。
func markdownCard(text string) string {
	card := map[string]any{
		"config":   map[string]any{"wide_screen_mode": true},
		"elements": []any{map[string]any{"tag": "markdown", "content": text}},
	}
	encoded, _ := json.Marshal(card)
	return string(encoded)
}
