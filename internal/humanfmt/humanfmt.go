// Package humanfmt formats what a person reads on the panel: a duration as
// Chinese words, and a long line shortened. It exists so one fact has one
// wording: the panel said "12 分前" where the digest said "12 分钟前", and a
// task's runtime had a third spelling.
//
// The relative times stay hand-written rather than coming from a library:
// github.com/dustin/go-humanize's RelTime says "12 minutes ago", and Chinese has
// no plural forms to build that from. Numbers with thousands separators do come
// from humanize, which is where that belongs.
package humanfmt

import (
	"fmt"
	"time"
)

// Age is a duration as the panel reads it, without a suffix: "刚刚", "12 分",
// "3 时", "2 天". A negative duration reads as "刚刚": a clock that is a little
// ahead of the event is still a thing that just happened.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 时", int(d.Hours()))
	}
	return fmt.Sprintf("%d 天", int(d.Hours()/24))
}

// Ago is Age as a point in the past: "刚刚", "12 分前", "3 时前", "2 天前".
// 刚刚 takes no 前.
func Ago(d time.Duration) string {
	if d < time.Minute {
		return "刚刚"
	}
	return Age(d) + "前"
}

// Until is a future duration as the panel reads it: "已到时间", "不到 1 分钟后",
// "20 分钟后", "3 小时后", "2 天后". Once the time has come it says so instead of
// showing a negative count.
func Until(d time.Duration) string {
	switch {
	case d <= 0:
		return "已到时间"
	case d < time.Minute:
		return "不到 1 分钟后"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟后", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时后", int(d.Hours()))
	}
	return fmt.Sprintf("%d 天后", int(d.Hours()/24))
}

// Duration is a span as a running task reads it: "刚刚", "12 分", "1 小时 5 分".
// The minutes are kept once hours are, because how long a worker has been going
// is the number a person is judging.
func Duration(d time.Duration) string {
	if d < time.Minute {
		return "刚刚"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分", int(d.Minutes()))
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) - hours*60
	if minutes == 0 {
		return fmt.Sprintf("%d 小时", hours)
	}
	return fmt.Sprintf("%d 小时 %d 分", hours, minutes)
}

// Clip shortens s to n runes, with an ellipsis, so one long line cannot stretch
// a row across the window. It counts runes, not bytes: a Chinese title cut by
// bytes would end in half a character.
func Clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	return string(r[:n]) + "…"
}
