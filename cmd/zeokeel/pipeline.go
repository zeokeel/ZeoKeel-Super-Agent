package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/zeokeel/super-agent/internal/workflow"
)

// pipelineFile is the JSON a person writes to describe a batch job:
//
//	{"name":"nightly","steps":[{"id":"pull","title":"拉代码","handler":"bash",
//	  "args":{"command":"git pull"}}]}
//
// It exists so a repeatable multi-step job does not need a chat message and a
// model: some things are better expressed once and run on a schedule.
type pipelineFile struct {
	Name  string `json:"name"`
	Steps []struct {
		ID      string            `json:"id"`
		Title   string            `json:"title"`
		Handler string            `json:"handler"`
		Args    map[string]string `json:"args"`
	} `json:"steps"`
}

func (p pipelineFile) definition() workflow.Definition {
	d := workflow.Definition{Name: p.Name}
	for _, s := range p.Steps {
		d.Steps = append(d.Steps, workflow.Step{
			ID: s.ID, Title: s.Title, Handler: s.Handler, Args: s.Args,
		})
	}
	return d
}

// runPipeline executes one pipeline file and prints what each step produced. It is
// a headless mode: no window, no model, exit code 0 only if every step succeeded.
func runPipeline(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pipeline:", err)
		return 2
	}
	var pf pipelineFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		fmt.Fprintln(os.Stderr, "pipeline:", err)
		return 2
	}

	st := workflow.NewMemoryStore()
	r, err := workflow.New(st)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pipeline:", err)
		return 2
	}
	if err := r.Register("bash", bashHandler); err != nil {
		fmt.Fprintln(os.Stderr, "pipeline:", err)
		return 2
	}

	run, err := r.Start(context.Background(), pf.definition(), map[string]string{"file": path})
	if run != nil {
		for _, s := range pf.Steps {
			status := run.StepStatus[s.ID]
			out := run.Outputs[s.ID]
			fmt.Printf("%-8s %-12s %s\n", status, s.ID, oneLine(out))
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pipeline failed:", err)
		return 1
	}
	return 0
}

// bashHandler runs one command in Args["command"]. Output is trimmed to something a
// terminal can read; a failed command fails the step, which stops the pipeline.
func bashHandler(ctx context.Context, in workflow.HandlerInput) (string, error) {
	command := in.Args["command"]
	if command == "" {
		return "", fmt.Errorf("bash step needs args.command")
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", command)
	cmd.Dir = in.Args["dir"]
	out, err := cmd.CombinedOutput()
	text := string(out)
	if len(text) > 2000 {
		text = text[:2000] + "\n…(truncated)"
	}
	if err != nil {
		return text, fmt.Errorf("%s: %w", command, err)
	}
	return text, nil
}

// oneLine keeps a step's output to one terminal line: the first line is what
// usually says whether it worked; the rest is for the log file.
func oneLine(s string) string {
	if i := indexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if len(s) > 100 {
		s = s[:100] + "…"
	}
	return s
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
