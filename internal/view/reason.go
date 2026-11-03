package view

import "strings"

// ReasonText renders a decision's reason code through its template. The panel
// shows the code as a monospace chip (it is what the policy logged) and this text
// beside it (it is what a person needs). A code with no template must not be
// replaced by a guess: it is shown as unrecovered so it can be added.
func ReasonText(code string) string {
	switch code {
	// What decided the message was worth doing.
	case "said":
		return "明确说了这是一项工作"
	case "routed":
		return "按工作区简报路由到这里"
	case "allowed_by_mode":
		return "命中允许名单且策略允许接单"
	case "started_ticket":
		return "已建工单开始执行"

	// What decided it was not.
	case "notification":
		return "判定为通知或公告，不是请求"
	case "not_addressed":
		return "没有点名 agent，只作上下文"
	case "not_a_member":
		return "发送者不在允许名单"
	case "clarify":
		return "请求不清楚，已经向对方追问"
	case "observed":
		return "接收策略是「观察」：只记录，不开单"
	case "accept_mode_off":
		return "接收策略是「关闭」：这类消息不看"
	case "reply_mode_notify":
		return "应答策略是「只通知你」：不直接回对方"
	case "rejected_by_person":
		return "你驳回了这条请求"
	case "sensitive_workspace":
		return "工作区带敏感约束，必须由人确认"
	}
	return unrecoveredReason(code)
}

// unrecoveredReason is deliberately explicit: showing the bare code as if it were
// a sentence hides a missing template, and a person reading it cannot tell whether
// the agent decided something or the panel broke.
func unrecoveredReason(code string) string {
	if strings.TrimSpace(code) == "" {
		return "没有记录判定原因"
	}
	return "这个判定码还没有文案：" + code
}
