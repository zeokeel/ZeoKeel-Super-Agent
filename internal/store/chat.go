package store

// SuperAgent chat persistence: the panel's recent conversation with the super
// agent. Runtime owns the chat's meaning (roles, rendering, which conversations
// exist); the store owns the storage contract and its bound — one kv row per chat
// id, JSON encoded, trimmed to a fixed message count and byte budget so a
// long-running install cannot grow the database without limit.
//
// This is deliberately a small dedicated API rather than "runtime writes a JSON
// blob into kv": the bound, the canonical timestamps, the duplicate-id rule and the
// "chat rows are not settings" separation are exactly the invariants that would
// otherwise be re-decided by every caller.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ChatMessage is one persisted chat turn. Role is kept as an opaque short string
// ("user", "assistant", "system", ...) so the runtime can evolve its vocabulary
// without a schema migration. ID is optional; when set, appending a message with an
// existing ID replaces it (a retried save is not a duplicate turn).
type ChatMessage struct {
	ID   string    `json:"id,omitempty"`
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

const (
	// chatKeyPrefix namespaces chat rows in kv, away from "setting:" and the
	// migration/schema keys. Settings() never returns them (it filters by prefix).
	chatKeyPrefix = "chat."
	// maxChatMessages is how many turns one chat row keeps; older turns are
	// dropped from the front. The panel shows a short recent history, not an
	// archive — the durable transcript of a Ticket lives on the Ticket.
	maxChatMessages = 200
	// maxChatBytes bounds one chat row after JSON encoding. A single huge message
	// is rune-truncated rather than allowed to blow the row past this.
	maxChatBytes = 256 << 10
	// maxChatIDLen bounds the chat id (it becomes part of a kv key).
	maxChatIDLen = 200
	// maxChatRoleLen bounds the role word.
	maxChatRoleLen = 32
	// maxChatTextRunes bounds one message's text (defence in depth on top of the
	// byte budget; ~64KB of CJK).
	maxChatTextRunes = 64 << 10
)

// PutChat replaces the whole bounded history of one chat. It is for callers that
// already hold the list (a projection rebuild, a test); incremental callers should
// use AppendChat so the read-modify-write happens in one transaction.
func (s *Store) PutChat(chatID string, messages []ChatMessage) error {
	id, err := normalizeChatID(chatID)
	if err != nil {
		return err
	}
	raw, err := s.encodeChat(messages)
	if err != nil {
		return err
	}
	return s.kvSet(chatKeyPrefix+id, raw)
}

// AppendChat adds one turn to a chat and returns the bounded history after the
// append. The read, the duplicate-id check and the write happen inside one
// transaction, so two writers on the command goroutine cannot lose a turn.
func (s *Store) AppendChat(chatID string, m ChatMessage) ([]ChatMessage, error) {
	id, err := normalizeChatID(chatID)
	if err != nil {
		return nil, err
	}
	msg, err := s.normalizeChatMessage(m)
	if err != nil {
		return nil, err
	}
	var out []ChatMessage
	err = s.Atomic(func() error {
		current, err := s.chatLocked(chatKeyPrefix + id)
		if err != nil {
			return err
		}
		replaced := false
		if msg.ID != "" {
			for i := range current {
				if current[i].ID == msg.ID {
					current[i] = msg
					replaced = true
					break
				}
			}
		}
		if !replaced {
			current = append(current, msg)
		}
		raw, err := s.encodeChat(current)
		if err != nil {
			return err
		}
		// Return what is actually stored (the encoder may have dropped old turns).
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return err
		}
		return s.kvSet(chatKeyPrefix+id, raw)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Chat returns the bounded history of one chat, oldest first. An unknown chat is
// (nil, nil): "no chat yet" is not an error worth failing a page over.
func (s *Store) Chat(chatID string) ([]ChatMessage, error) {
	id, err := normalizeChatID(chatID)
	if err != nil {
		return nil, err
	}
	return s.chatLocked(chatKeyPrefix + id)
}

// ClearChat removes one chat's history. The chat id may be reused afterwards; no
// tombstone is needed because a chat row carries no business identity.
func (s *Store) ClearChat(chatID string) error {
	id, err := normalizeChatID(chatID)
	if err != nil {
		return err
	}
	_, err = s.Exec(`DELETE FROM kv WHERE k=?`, chatKeyPrefix+id)
	return err
}

// ChatIDs lists the chats that have stored history, sorted by id. The runtime uses
// it to project the conversation list without opening every row.
func (s *Store) ChatIDs() ([]string, error) {
	rows, err := s.Query(`SELECT k FROM kv WHERE k LIKE ? ORDER BY k`, chatKeyPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, strings.TrimPrefix(k, chatKeyPrefix))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(out) // SQL ORDER BY already sorts; kept for a stable contract.
	return out, nil
}

// chatLocked decodes one chat row. It must be called on the store's command
// goroutine (or inside a transaction when writing).
func (s *Store) chatLocked(key string) ([]ChatMessage, error) {
	raw, err := s.kvGet(key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []ChatMessage
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("store: chat row %q is not readable JSON: %w", key, err)
	}
	return out, nil
}

// encodeChat normalizes, bounds and JSON-encodes one history.
func (s *Store) encodeChat(messages []ChatMessage) (string, error) {
	bounded := make([]ChatMessage, 0, len(messages))
	for _, m := range messages {
		msg, err := s.normalizeChatMessage(m)
		if err != nil {
			return "", err
		}
		bounded = append(bounded, msg)
	}
	// Keep the newest maxChatMessages turns; the rest is not "recent chat".
	if len(bounded) > maxChatMessages {
		bounded = bounded[len(bounded)-maxChatMessages:]
	}
	// Drop the oldest turns until the row fits the byte budget, then truncate the
	// single remaining message's text if it alone is too large.
	raw, err := json.Marshal(bounded)
	if err != nil {
		return "", err
	}
	for len(raw) > maxChatBytes && len(bounded) > 1 {
		bounded = bounded[1:]
		if raw, err = json.Marshal(bounded); err != nil {
			return "", err
		}
	}
	for len(raw) > maxChatBytes && len(bounded) == 1 && bounded[0].Text != "" {
		// One message larger than the whole budget: halve its text on a rune
		// boundary until the row fits. The panel loses the tail of a giant pasted
		// message, not the row.
		bounded[0].Text = truncateChatText(bounded[0].Text, len(bounded[0].Text)/2)
		if raw, err = json.Marshal(bounded); err != nil {
			return "", err
		}
	}
	return string(raw), nil
}

// normalizeChatMessage validates one turn and canonicalizes its timestamp.
func (s *Store) normalizeChatMessage(m ChatMessage) (ChatMessage, error) {
	role := strings.TrimSpace(m.Role)
	if role == "" {
		return ChatMessage{}, fmt.Errorf("store: chat message needs a role")
	}
	if len(role) > maxChatRoleLen {
		return ChatMessage{}, fmt.Errorf("store: chat role is longer than %d bytes", maxChatRoleLen)
	}
	if m.At.IsZero() {
		m.At = s.clock().UTC()
	} else {
		m.At = m.At.UTC()
	}
	m.Role = role
	m.ID = strings.TrimSpace(m.ID)
	if utf8.RuneCountInString(m.Text) > maxChatTextRunes {
		m.Text = truncateChatText(m.Text, maxChatTextRunes)
	}
	return m, nil
}

// normalizeChatID validates a chat id: it becomes a kv key suffix, so control
// characters and separators that would make the key unreadable are refused.
func normalizeChatID(chatID string) (string, error) {
	id := strings.TrimSpace(chatID)
	if id == "" {
		return "", fmt.Errorf("store: chat id is required")
	}
	if len(id) > maxChatIDLen {
		return "", fmt.Errorf("store: chat id is longer than %d bytes", maxChatIDLen)
	}
	for _, r := range id {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return "", fmt.Errorf("store: chat id %q contains a control character or path separator", id)
		}
	}
	return id, nil
}

// truncateChatText cuts s to at most max bytes on a rune boundary.
func truncateChatText(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return strings.ToValidUTF8(s[:max], "")
}
