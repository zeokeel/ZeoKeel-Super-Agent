package browser

import (
	"os"
	"runtime"
	"strings"
)

// Detect returns the supported browsers found at their documented default
// install paths (no PATH search, no user-profile probing). Chrome is preferred
// over Edge; within one browser the first existing candidate wins.
func Detect() []Browser {
	return detect(runtime.GOOS, fileExists, os.Getenv)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// detect is the testable form: goos selects the candidate table, exists fakes
// the filesystem and env fakes the Windows/macOS user directories.
func detect(goos string, exists func(string) bool, env func(string) string) []Browser {
	type candidate struct {
		id   ID
		name string
		path string
	}
	var candidates []candidate
	switch goos {
	case "darwin":
		home := env("HOME")
		candidates = []candidate{
			{Chrome, "Google Chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
			{Chrome, "Google Chrome", home + "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
			{Edge, "Microsoft Edge", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
			{Edge, "Microsoft Edge", home + "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
		}
	case "windows":
		programFiles := env("PROGRAMFILES")
		if programFiles == "" {
			programFiles = `C:\Program Files`
		}
		programFilesX86 := env("PROGRAMFILES(X86)")
		if programFilesX86 == "" {
			programFilesX86 = `C:\Program Files (x86)`
		}
		localAppData := env("LOCALAPPDATA")
		candidates = []candidate{
			{Chrome, "Google Chrome", programFiles + `\Google\Chrome\Application\chrome.exe`},
			{Chrome, "Google Chrome", programFilesX86 + `\Google\Chrome\Application\chrome.exe`},
			{Chrome, "Google Chrome", localAppData + `\Google\Chrome\Application\chrome.exe`},
			{Edge, "Microsoft Edge", programFilesX86 + `\Microsoft\Edge\Application\msedge.exe`},
			{Edge, "Microsoft Edge", programFiles + `\Microsoft\Edge\Application\msedge.exe`},
			{Edge, "Microsoft Edge", localAppData + `\Microsoft\Edge\Application\msedge.exe`},
		}
	default: // linux and other unix
		candidates = []candidate{
			{Chrome, "Google Chrome", "/usr/bin/google-chrome"},
			{Chrome, "Google Chrome", "/usr/bin/google-chrome-stable"},
			{Chrome, "Google Chrome", "/opt/google/chrome/chrome"},
			{Chrome, "Google Chrome", "/snap/bin/chromium"},
			{Edge, "Microsoft Edge", "/usr/bin/microsoft-edge"},
			{Edge, "Microsoft Edge", "/usr/bin/microsoft-edge-stable"},
			{Edge, "Microsoft Edge", "/opt/microsoft/msedge/msedge"},
		}
	}
	found := map[ID]bool{}
	var out []Browser
	for _, c := range candidates {
		if c.path == "" || found[c.id] {
			continue
		}
		if exists(c.path) {
			out = append(out, Browser{ID: c.id, Name: c.name, Path: c.path})
			found[c.id] = true
		}
	}
	return out
}

// browserByID returns the detected browser with the requested id, or the first
// detected one when id is empty (Chrome preferred because detect orders it
// first).
func browserByID(detected []Browser, id string) (Browser, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		if len(detected) == 0 {
			return Browser{}, false
		}
		return detected[0], true
	}
	for _, b := range detected {
		if string(b.ID) == id {
			return b, true
		}
	}
	return Browser{}, false
}
