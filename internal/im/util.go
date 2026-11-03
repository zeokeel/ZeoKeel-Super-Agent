package im

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// DeriveKey 用内容派生一个稳定的幂等键：当 Outbox 没有给 key（例如本地
// 直接回写）时，驱动用它保证"同一条消息重发两次只有一条"。内容派生而不
// 是随机 ID，是为了崩溃重放时能算出同一个 key（§4.4 不变量 1）。
func DeriveKey(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0}) // 分隔符：避免 ("ab","c") 与 ("a","bc") 撞车
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// Truncate 按 rune 截断到 max，尾部补省略号。按 rune 而不是 byte 是因为
// 中文消息按字节切会把一个字切成非法 UTF-8，IM 侧会直接拒收或显示乱码。
func Truncate(text string, max int) string {
	if max <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

// CollapseSpaces 把一个字符串里的连续空白压成一个空格并去掉首尾空白。
// 渲染 @占位符后经常留下双空格，压一下让模型看到的文本干净。
func CollapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
