package im

import "github.com/zeokeel/super-agent/internal/domain"

// AddressedFor 把驱动事实翻译成领域的三元事实（domain.Addressed）。
//
// 为什么放在公共包而不是每个驱动各写一份：这三条规则是领域纪律，不是协议
// 细节，任何新 IM 都必须一样，否则"群里 @ 同事"会被当成对 agent 说话：
//   - Mention：只有本条消息 @ 了机器人自己才算；@所有人 在驱动层已经从
//     Mentions 里剔除（飞书的 @_all、钉钉的 @all 都不是一个人）；
//   - Direct：单聊这个会话语义本身就是"直接对 agent 说"；
//   - ReplyTo：只有确证被回复的消息属于机器人（ReplyToBot）才算事实，
//     回复别人只是回复链，不构成"对 agent 说话"（§4.9 / 001）。
func AddressedFor(ev RawEvent, selfID string) domain.Addressed {
	return domain.Addressed{
		Mention: Mentioned(ev, selfID),
		Direct:  ev.Conv.Kind == domain.ConvDirect,
		ReplyTo: ev.ReplyToBot,
	}
}

// Mentioned 判断本条消息是否 @ 了 selfID。空 id 永不算：飞书 @所有人 的
// open_id 是空串、钉钉 atUsers 里也可能给空 staffId，用空串比较会误命中。
func Mentioned(ev RawEvent, selfID string) bool {
	if selfID == "" {
		return false
	}
	for _, id := range ev.Mentions {
		if id == selfID {
			return true
		}
	}
	return false
}
