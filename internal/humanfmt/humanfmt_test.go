package humanfmt

import (
	"strings"
	"testing"
	"time"
)

// The panel's wording, pinned table by table: these strings are what a person
// reads, and the three copies this package replaced had drifted apart.
func TestAge(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "刚刚"},
		{0, "刚刚"},
		{59 * time.Second, "刚刚"},
		{time.Minute, "1 分"},
		{12 * time.Minute, "12 分"},
		{59 * time.Minute, "59 分"},
		{time.Hour, "1 时"},
		{23*time.Hour + 59*time.Minute, "23 时"},
		{24 * time.Hour, "1 天"},
		{72 * time.Hour, "3 天"},
	} {
		if got := Age(tc.d); got != tc.want {
			t.Errorf("Age(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestAgo(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "刚刚"},
		{30 * time.Second, "刚刚"},
		{time.Minute, "1 分前"},
		{12 * time.Minute, "12 分前"},
		{2 * time.Hour, "2 时前"},
		{25 * time.Hour, "1 天前"},
	} {
		if got := Ago(tc.d); got != tc.want {
			t.Errorf("Ago(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestUntil(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{-time.Minute, "已到时间"},
		{0, "已到时间"},
		{30 * time.Second, "不到 1 分钟后"},
		{time.Minute, "1 分钟后"},
		{2 * time.Hour, "2 小时后"},
		{26 * time.Hour, "1 天后"},
	} {
		if got := Until(tc.d); got != tc.want {
			t.Errorf("Until(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestDuration(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "刚刚"},
		{12 * time.Minute, "12 分"},
		{time.Hour, "1 小时"},
		{65 * time.Minute, "1 小时 5 分"},
		{3*time.Hour + 40*time.Minute, "3 小时 40 分"},
	} {
		if got := Duration(tc.d); got != tc.want {
			t.Errorf("Duration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// Clip counts runes: a Chinese title must not be cut in the middle of a
// character, and a clipped title keeps its ending as an ellipsis.
func TestClip(t *testing.T) {
	for _, tc := range []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly-10", 10, "exactly-10"},
		{"0123456789ab", 10, "0123456789…"},
		{"修改登录超时配置并合入主干", 8, "修改登录超时配置…"},
		{"abc", 0, ""},
	} {
		got := Clip(tc.s, tc.n)
		if got != tc.want {
			t.Errorf("Clip(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
		if n := len([]rune(got)); tc.n > 0 && n > tc.n+1 {
			t.Errorf("Clip(%q, %d) returned %d runes, too long", tc.s, tc.n, n)
		}
		if strings.Contains(got, "\ufffd") {
			t.Errorf("Clip(%q, %d) broke a character: %q", tc.s, tc.n, got)
		}
	}
}
