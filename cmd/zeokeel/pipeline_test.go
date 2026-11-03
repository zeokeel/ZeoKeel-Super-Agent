package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A pipeline is a batch job: steps run in order, output is visible, and a failing
// step stops the run with a non-zero exit (that is what a cron job checks).
func TestPipelineRunsStepsAndReports(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	file := filepath.Join(dir, "p.json")
	body := `{"name":"check","steps":[
	  {"id":"write","title":"写","handler":"bash","args":{"command":"echo one > ` + out + `"}},
	  {"id":"read","title":"读","handler":"bash","args":{"command":"cat ` + out + `"}}
	]}`
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runPipeline(file); code != 0 {
		t.Fatalf("exit %d", code)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the first step did not run: %v", err)
	}
	if strings.TrimSpace(string(got)) != "one" {
		t.Fatalf("unexpected content %q", got)
	}
}

func TestPipelineStopsOnFailure(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "p.json")
	body := `{"name":"check","steps":[
	  {"id":"boom","title":"炸","handler":"bash","args":{"command":"exit 3"}},
	  {"id":"after","title":"不该跑到","handler":"bash","args":{"command":"echo ran"}}
	]}`
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runPipeline(file); code == 0 {
		t.Fatal("a failing step must make the pipeline exit non-zero")
	}
}

func TestPipelineRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.json")
	if code := runPipeline(missing); code == 0 {
		t.Fatal("a missing file must fail")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runPipeline(bad); code == 0 {
		t.Fatal("invalid JSON must fail")
	}
}
