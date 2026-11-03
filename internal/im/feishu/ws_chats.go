package feishu

// WS chat scope (UX IM setup): the long-connection stream carries every message
// the app can see, so chats act as a filter, not as an API requirement.
//
//   - empty Chats: listen to everything the app receives (DMs and @-mentions
//     included; the addressed facts still decide whether a message is for the
//     agent);
//   - non-empty Chats: only events from those chat ids become RawEvents.
//
// HTTP long polling keeps its own contract: it polls the configured chats, so
// longpoll requires at least one chat id (the runtime rejects an empty list).

import "strings"

// wsChatAllowed reports whether one WS message's chat is in scope.
func (d *Driver) wsChatAllowed(chatID string) bool {
	if len(d.cfg.Chats) == 0 {
		return true
	}
	chatID = strings.TrimSpace(chatID)
	for _, chat := range d.cfg.Chats {
		if strings.TrimSpace(chat.ID) == chatID {
			return true
		}
	}
	return false
}
