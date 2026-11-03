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
				{ID: "longpoll", Label: "长轮询", Implemented: true},
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
