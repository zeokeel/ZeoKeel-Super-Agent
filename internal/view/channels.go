package view

// 设置 › 来源 的两个 IM 通道：飞书与钉钉。Their configuration lives in the 来源
// settings group (so saving keeps the one settings protocol); this file is the
// panel's fixed knowledge of which keys belong to which channel and which
// transports exist — facts of this build, shown with their key names so a person
// can tell "not projected yet" from "not configured".

// ChannelOption is one transport a channel can use. Implemented false means this
// build has no driver for it yet: the panel shows it with 未实现 and will not
// select it, so nobody configures a transport that cannot run.
type ChannelOption struct {
	ID          string // "longpoll", "dws", "stream"
	Label       string // 长轮询, DWS 长连接, 官方 Stream
	Implemented bool
	// Note explains a not-implemented option in one line.
	Note string
}

// ChannelSpec is one IM channel: its label, the 来源-group keys it reads, and the
// transports it knows.
type ChannelSpec struct {
	// Name is the id the runtime's ChannelAuthorizer uses: "feishu", "dingtalk".
	Name  string
	Label string
	// EnabledKey and TransportKey are the 来源-group field keys.
	EnabledKey   string
	TransportKey string
	// CredentialKeys are the 来源-group field keys of its credentials (secret
	// fields are masked by the runtime; the panel never stores them).
	CredentialKeys []string
	// Options are the transports, in the order they show. A runtime that
	// publishes the transport field's Options replaces them all as enabled.
	Options []ChannelOption
}

// ChannelSpecs is the channels this build knows, in the order the page shows
// them. 飞书 is long-poll; 钉钉 is DWS or the official Stream SDK, both built by
// the runtime (runtime/im.go), so both are selectable.
func ChannelSpecs() []ChannelSpec {
	return []ChannelSpec{
		{
			Name: "feishu", Label: "飞书",
			EnabledKey: "feishu_enabled", TransportKey: "feishu_transport",
			CredentialKeys: []string{"feishu_app_id", "feishu_app_secret", "feishu_chats"},
			Options: []ChannelOption{
				{ID: "ws", Label: "官方长连接", Implemented: true,
					Note: "飞书官方 larkws 长连接（推荐）；需要网络可达飞书"},
				{ID: "longpoll", Label: "HTTP 长轮询", Implemented: true,
					Note: "显式兜底；保留游标"},
			},
		},
		{
			Name: "dingtalk", Label: "钉钉",
			EnabledKey: "dingtalk_enabled", TransportKey: "dingtalk_transport",
			CredentialKeys: []string{"dingtalk_app_key", "dingtalk_app_secret", "dingtalk_robot"},
			Options: []ChannelOption{
				{ID: "dws", Label: "DWS 长连接", Implemented: true},
				{ID: "stream", Label: "官方 Stream", Implemented: true,
					Note: "官方 Stream SDK；需要 app key / secret / robot code"},
			},
		},
	}
}

// ChannelSpecOf returns one channel by name, or nil.
func ChannelSpecOf(name string) *ChannelSpec {
	specs := ChannelSpecs()
	for i := range specs {
		if specs[i].Name == name {
			return &specs[i]
		}
	}
	return nil
}

// ChannelKey reports whether a 来源-group field key belongs to one of the IM
// channels: the settings page draws those inside the channel cards, not twice.
func ChannelKey(key string) bool {
	for _, spec := range ChannelSpecs() {
		if key == spec.EnabledKey || key == spec.TransportKey {
			return true
		}
		for _, k := range spec.CredentialKeys {
			if key == k {
				return true
			}
		}
	}
	return false
}

// ChannelFieldSpec is one input of a connection wizard step.
type ChannelFieldSpec struct {
	Key   string
	Label string
	Help  string
	// Kind is "text", "secret", "switch" or "select"; empty means text.
	Kind     string
	Required bool
	Options  []string
	Default  string
}

// ChannelTransportSpec is one way to connect a channel, with only the fields that
// transport needs (a DWS profile is meaningless for Stream, and vice versa).
type ChannelTransportSpec struct {
	ID          string
	Label       string
	Implemented bool
	Fields      []ChannelFieldSpec
}

// ChannelTransports lists a channel's transports and their fields. It is the
// panel's fallback when the runtime does not publish per-transport metadata, so a
// wizard is still usable without a runtime adapter.
func ChannelTransports(name string) []ChannelTransportSpec {
	switch name {
	case "feishu":
		return []ChannelTransportSpec{
			{ID: "ws", Label: "官方长连接（推荐）", Implemented: true, Fields: []ChannelFieldSpec{
				{Key: "feishu_app_id", Label: "App ID", Required: true},
				{Key: "feishu_app_secret", Label: "App Secret", Kind: "secret", Required: true},
				{Key: "feishu_chats", Label: "监听会话（可选）", Help: "逗号分隔；留空 = 全部可用的群/@你"},
			}},
			{ID: "longpoll", Label: "HTTP 长轮询（兜底）", Implemented: true, Fields: []ChannelFieldSpec{
				{Key: "feishu_app_id", Label: "App ID", Required: true},
				{Key: "feishu_app_secret", Label: "App Secret", Kind: "secret", Required: true},
				{Key: "feishu_chats", Label: "监听会话（必填）", Help: "逗号分隔的群 ID", Required: true},
			}},
		}
	case "dingtalk":
		return []ChannelTransportSpec{
			{ID: "dws", Label: "DWS（本机命令行）", Implemented: true, Fields: []ChannelFieldSpec{
				{Key: "dingtalk_dws_cmd", Label: "dws 命令", Default: "dws"},
				{Key: "dingtalk_profile", Label: "profile"},
				{Key: "dingtalk_listen_kind", Label: "监听类型", Kind: "select",
					Options: []string{"im", "event"}, Default: "im"},
				{Key: "dingtalk_chat_query", Label: "会话查询（可选）"},
			}},
			{ID: "stream", Label: "官方 Stream（推荐）", Implemented: true, Fields: []ChannelFieldSpec{
				{Key: "dingtalk_app_key", Label: "AppKey", Required: true},
				{Key: "dingtalk_app_secret", Label: "AppSecret", Kind: "secret", Required: true},
				{Key: "dingtalk_robot", Label: "RobotCode", Required: true},
				{Key: "dingtalk_bot_id", Label: "机器人自身 ID（@ 判定）", Required: true},
			}},
		}
	}
	return nil
}

// ChannelTransportOf finds one transport of a channel.
func ChannelTransportOf(name, id string) (ChannelTransportSpec, bool) {
	for _, t := range ChannelTransports(name) {
		if t.ID == id {
			return t, true
		}
	}
	return ChannelTransportSpec{}, false
}

// ChannelPauseListenKey is the switch that pauses listening while sending, so a
// channel cannot feed its own messages back in.
const ChannelPauseListenKey = "dingtalk_pause_listen_on_send"
