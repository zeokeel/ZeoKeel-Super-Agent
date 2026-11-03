package main

import (
	"strings"
	"testing"
)

// -allow 必须真的能解析：历史实现用 fmt.Sscanf("%[^:]") 必然失败。
func TestParseAllow(t *testing.T) {
	source, actor, err := parseAllow("dingtalk:ding_zhangsan")
	if err != nil || source != "dingtalk" || actor != "ding_zhangsan" {
		t.Fatalf("parseAllow = %q/%q/%v", source, actor, err)
	}
	source, actor, err = parseAllow(" github:alice ")
	if err != nil || source != "github" || actor != "alice" {
		t.Fatalf("trimmed parseAllow = %q/%q/%v", source, actor, err)
	}
	for _, bad := range []string{"", "no-colon", ":actor", "source:", "  "} {
		if _, _, err := parseAllow(bad); err == nil {
			t.Errorf("parseAllow(%q) accepted an invalid value", bad)
		}
	}
	// 冒号在 actor 里不是错误（有些 id 自带冒号时只按第一段切）。
	if _, actor, err := parseAllow("github:bot:extra"); err != nil || actor != "bot:extra" {
		t.Fatalf("actor with colon = %q, %v", actor, err)
	}
}

func TestSplitCSVSkipsEmptyItems(t *testing.T) {
	got := splitCSV("webapp, 登录, ,账单,")
	want := []string{"webapp", "登录", "账单"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("splitCSV = %v, want %v", got, want)
	}
	if len(splitCSV("")) != 0 {
		t.Fatalf("splitCSV(\"\") = %v, want empty", splitCSV(""))
	}
}
