package browser

import (
	"runtime"
	"testing"
)

func fakeFS(paths ...string) func(string) bool {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

func TestDetectDefaultPathsLinux(t *testing.T) {
	got := detect("linux", fakeFS("/usr/bin/google-chrome-stable", "/usr/bin/microsoft-edge"), func(string) string { return "" })
	if len(got) != 2 || got[0].ID != Chrome || got[1].ID != Edge {
		t.Fatalf("detected = %+v", got)
	}
	if got[0].Path != "/usr/bin/google-chrome-stable" || got[1].Path != "/usr/bin/microsoft-edge" {
		t.Fatalf("paths = %+v", got)
	}
	if got[0].Name == "" || got[1].Name == "" {
		t.Fatalf("names = %+v", got)
	}
}

func TestDetectOnlyDocumentedDefaultPaths(t *testing.T) {
	// A browser installed anywhere else is not detected (no PATH search).
	if got := detect("linux", fakeFS("/usr/local/bin/google-chrome", "/home/me/.local/bin/msedge"), func(string) string { return "" }); len(got) != 0 {
		t.Fatalf("non-default path was detected: %+v", got)
	}
	// Chrome wins over Edge when both exist; only one entry per engine.
	got := detect("linux", fakeFS("/usr/bin/google-chrome", "/usr/bin/microsoft-edge"), func(string) string { return "" })
	if len(got) != 2 || got[0].ID != Chrome {
		t.Fatalf("order = %+v", got)
	}
}

func TestDetectMacOS(t *testing.T) {
	home := "/Users/me"
	got := detect("darwin", fakeFS(
		home+"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	), func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	})
	if len(got) != 2 {
		t.Fatalf("detected = %+v", got)
	}
	if got[0].ID != Chrome || got[1].ID != Edge {
		t.Fatalf("ids = %+v", got)
	}
}

func TestDetectWindowsExpandsProgramFiles(t *testing.T) {
	got := detect("windows", fakeFS(
		`D:\Apps\Google\Chrome\Application\chrome.exe`,
		`C:\Users\me\AppData\Local\Microsoft\Edge\Application\msedge.exe`,
	), func(k string) string {
		switch k {
		case "PROGRAMFILES":
			return `D:\Apps`
		case "PROGRAMFILES(X86)":
			return `D:\Apps (x86)`
		case "LOCALAPPDATA":
			return `C:\Users\me\AppData\Local`
		}
		return ""
	})
	if len(got) != 2 || got[0].Path != `D:\Apps\Google\Chrome\Application\chrome.exe` {
		t.Fatalf("windows detected = %+v", got)
	}
	if got[1].Path != `C:\Users\me\AppData\Local\Microsoft\Edge\Application\msedge.exe` {
		t.Fatalf("edge path = %q", got[1].Path)
	}
}

func TestDetectNoBrowsers(t *testing.T) {
	if got := detect("linux", fakeFS(), func(string) string { return "" }); len(got) != 0 {
		t.Fatalf("empty fs detected %+v", got)
	}
	// The real host may or may not have a browser; Detect must stay safe.
	for _, b := range Detect() {
		if b.ID != Chrome && b.ID != Edge {
			t.Fatalf("unexpected real detection: %+v", b)
		}
	}
	_ = runtime.GOOS
}

func TestBrowserByID(t *testing.T) {
	list := []Browser{{ID: Chrome, Path: "/chrome"}, {ID: Edge, Path: "/edge"}}
	if b, ok := browserByID(list, "edge"); !ok || b.Path != "/edge" {
		t.Fatalf("edge lookup = %+v/%v", b, ok)
	}
	if b, ok := browserByID(list, ""); !ok || b.ID != Chrome {
		t.Fatalf("default lookup = %+v/%v", b, ok)
	}
	if _, ok := browserByID(list, "safari"); ok {
		t.Fatal("safari was matched")
	}
	if _, ok := browserByID(nil, ""); ok {
		t.Fatal("empty detection returned a browser")
	}
}
