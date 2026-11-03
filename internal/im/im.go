// Package im is the one abstraction every instant-messaging tool implements:
// Feishu, DingTalk, Slack and Telegram are drivers behind these interfaces, not
// separate workflows. See design §4.8.
//
// Desktop constraint: the app sits behind NAT and cannot receive a public
// webhook, so receiving is a long-lived connection or long polling.
package im

import (
	"context"
	"errors"

	"github.com/zeokeel/super-agent/internal/domain"
)

// Capabilities describes what one messaging tool can render, so the Router
// adapts a reply instead of sending one "universal" message. This is the most
// underrated part of the abstraction: Telegram takes a little MarkdownV2,
// Feishu takes cards, a WeCom group bot takes plain text.
type Capabilities struct {
	Markdown     bool
	RichCard     bool
	Files        bool
	Threads      bool
	Mentions     bool
	ReadReceipt  bool
	SupportsEdit bool
	MaxTextLen   int
}

// Identity is the bot's own account on that tool.
type Identity struct {
	ID   string
	Name string
}

// Member is one person, used to sync an allowed list.
type Member struct {
	ID   string
	Name string
	Bot  bool
}

// RawEvent is a message as the driver received it, before translation.
type RawEvent struct {
	ID       string
	Conv     domain.Conversation
	Actor    domain.Actor
	Mentions []string // ids mentioned; the driver resolves @everyone away
	ReplyTo  string
	// AddressedToBot 是传输层给出的权威事实：这条消息明确是对 agent 本人说的
	// （DWS 的 at-me/group 监听、其它协议里能确证的 @）。空/假表示未确证；驱动
	// 仍可用 Mentions 比较 selfID。AddressedFor 会把它并进 Mention 事实。
	AddressedToBot bool
	// ReplyToBot 为真表示被回复的那条消息是本机器人自己发的（不是任意回复）。
	// 领域里 Addressed.ReplyTo 是一个"事实"：只有确证父消息属于 agent 才置位，
	// 否则一条"回复别人"的消息会被误判成对 agent 说话（§4.9 / 001）。
	ReplyToBot bool
	// Unsupported 为真表示这是一条本驱动还渲染不了的消息（贴纸、语音、位置…）。
	// 保留事件是为了让面板能解释"为什么这条没进来"，Source 不应据此建 Ticket。
	Unsupported bool
	Text        string
	MediaRef    []string
	At          int64 // unix millis, as the tool reports it
}

// Outbound is one message to send. Key is the idempotency key: a retry must not
// duplicate a message, so the driver passes it where the tool supports it.
type Outbound struct {
	Key      string
	Conv     domain.Conversation
	ReplyTo  string
	Text     string
	Markdown bool
	Files    []Attachment
}

// Attachment is a file to send.
type Attachment struct {
	Name string
	Mime string
	Data []byte
}

// Receipt is the outcome of a send.
type Receipt struct {
	MessageID string
	Gone      bool // permanently undeliverable; the Outbox drops it and stops retrying
}

// ErrGone marks a message that can never be delivered, such as a reply to a
// deleted conversation.
var ErrGone = errors.New("im: conversation gone")

// Provider is what every driver implements.
type Provider interface {
	Name() string
	Capabilities() Capabilities
	Self(ctx context.Context) (Identity, error)
	Members(ctx context.Context, convID string) ([]Member, error)
}

// Receiver receives a long-lived connection. Preferred on desktop.
type Receiver interface {
	Subscribe(ctx context.Context, out chan<- RawEvent) error
}

// Poller receives by long polling. A fallback for tools without a socket.
type Poller interface {
	Poll(ctx context.Context, cursor string) (events []RawEvent, next string, err error)
}

// TextSender is an optional transport capability for gateways whose send API is
// "plain text + a business idempotency key" (e.g. the DingTalk DWS CLI). The
// transport owns the hidden de-dup marker; the driver passes the business key
// and plain text. Drivers should prefer this over SendRobot when available.
type TextSender interface {
	SendText(ctx context.Context, conversationID, text, idempotencyKey string) (messageID string, err error)
}

// CursorReceiver is an optional Receiver capability for transports whose read
// position lives outside the process (DWS gateways, feishu long polling after a
// restart). The caller supplies the persisted cursor and a save callback that
// runs only after a batch was fully delivered; on save error the loop stops
// without advancing (the batch is re-read, duplicates are absorbed by
// content-derived ids).
type CursorReceiver interface {
	SubscribeFrom(ctx context.Context, cursor string, save func(next string) error, out chan<- RawEvent) error
}

// Sender writes to the tool and reads context back.
type Sender interface {
	Send(ctx context.Context, msg Outbound) (Receipt, error)
	ReadThread(ctx context.Context, conv domain.Conversation, cursor string) (Page, error)
}

// Page is one page of conversation context, for an assessment that needs more
// than the Trigger's own text.
type Page struct {
	Messages []RawEvent
	Next     string
	Done     bool
}

// Driver is a complete provider: the four interfaces above plus a name.
type Driver struct {
	Provider
	Receiver // may be nil
	Poller   // may be nil
	Sender   // may be nil
}

// EffectiveCapabilities returns the declared capabilities, filling in a sane
// text limit when a driver forgets to.
func EffectiveCapabilities(p Provider) Capabilities {
	c := p.Capabilities()
	if c.MaxTextLen == 0 {
		c.MaxTextLen = 4000
	}
	return c
}

// ErrClosed marks a driver/transport that was closed. Lifecycle owners
// (runtime) should treat it as "stopped", not as a transient failure to retry.
var ErrClosed = errors.New("im: closed")

// Closer is an optional capability: a driver that owns goroutines, timers or
// child processes implements it so the runtime can stop it deterministically
// (Close is idempotent; subsequent Receive/Subscribe calls fail with ErrClosed).
type Closer interface {
	Close() error
}

// ErrUnsupported marks a capability the driver cannot provide on this tool:
// DingTalk has no history API, so ReadThread answers it instead of pretending an
// empty page is a real read (an empty page would silently hide context).
var ErrUnsupported = errors.New("im: capability not supported by this tool")
