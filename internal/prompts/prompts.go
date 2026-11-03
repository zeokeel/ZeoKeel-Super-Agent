// Package prompts composes system prompts out of ordered sections.
//
// Why not one big string per agent: a prompt is a contract with several parties —
// the model, the person reading the panel, the tools that must be called, and the
// deployment that may want its own voice. Composing named sections keeps each
// paragraph owned by exactly one concern, makes an override replace one section
// instead of everything, and lets tests pin the invariants that must never be lost
// (design §5.3). dsh composes its system prompt the same way, with ordered sections
// and explicit slots for a deployment persona.
package prompts

import (
	"sort"
	"strings"
)

// Section is one named paragraph of a prompt.
type Section struct {
	// Name identifies the paragraph for overrides and tests ("identity", "tools").
	Name string
	// Order is the position; lower first. Gaps are fine.
	Order int
	// Body is the text, with optional {{variable}} references.
	Body string
}

// Set is an ordered collection of sections.
type Set struct {
	sections []Section
}

// New builds a set from sections, sorted by order then name (a stable sort: two
// sections with the same order must not swap between runs).
func New(sections ...Section) *Set {
	s := &Set{sections: append([]Section(nil), sections...)}
	sort.SliceStable(s.sections, func(i, j int) bool {
		if s.sections[i].Order != s.sections[j].Order {
			return s.sections[i].Order < s.sections[j].Order
		}
		return s.sections[i].Name < s.sections[j].Name
	})
	return s
}

// Add appends or replaces a section by name, keeping the set sorted.
func (s *Set) Add(sec Section) {
	for i := range s.sections {
		if s.sections[i].Name == sec.Name {
			s.sections[i] = sec
			s.resort()
			return
		}
	}
	s.sections = append(s.sections, sec)
	s.resort()
}

func (s *Set) resort() {
	sort.SliceStable(s.sections, func(i, j int) bool {
		if s.sections[i].Order != s.sections[j].Order {
			return s.sections[i].Order < s.sections[j].Order
		}
		return s.sections[i].Name < s.sections[j].Name
	})
}

// Remove drops a section by name, for a deployment that wants to suppress one.
func (s *Set) Remove(name string) {
	out := s.sections[:0]
	for _, sec := range s.sections {
		if sec.Name != name {
			out = append(out, sec)
		}
	}
	s.sections = out
}

// Names lists the section names in render order.
func (s *Set) Names() []string {
	out := make([]string, 0, len(s.sections))
	for _, sec := range s.sections {
		out = append(out, sec.Name)
	}
	return out
}

// Get returns one section body.
func (s *Set) Get(name string) (string, bool) {
	for _, sec := range s.sections {
		if sec.Name == name {
			return sec.Body, true
		}
	}
	return "", false
}

// Expand replaces {{variable}} references in an arbitrary prompt body. The panel's
// prompt editor shows templates, so a saved override must be expanded with the same
// variables the built-in prompt uses.
func Expand(body string, vars map[string]string) string { return expand(body, vars) }

// Render joins the sections, expanding {{variable}} references. An unknown
// variable is left as written rather than silently blanked: a prompt with a visible
// "{{workspace}}" is a bug report, while a blank line is a mystery.
func (s *Set) Render(vars map[string]string) string {
	var b strings.Builder
	for i, sec := range s.sections {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(expand(sec.Body, vars))
	}
	return b.String()
}

// expand replaces {{name}} references one pass, so a value containing braces is not
// re-expanded.
func expand(body string, vars map[string]string) string {
	if len(vars) == 0 {
		return body
	}
	var b strings.Builder
	for {
		i := strings.Index(body, "{{")
		if i < 0 {
			b.WriteString(body)
			break
		}
		j := strings.Index(body[i:], "}}")
		if j < 0 {
			b.WriteString(body)
			break
		}
		b.WriteString(body[:i])
		name := strings.TrimSpace(body[i+2 : i+j])
		if v, ok := vars[name]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(body[i : i+j+2])
		}
		body = body[i+j+2:]
	}
	return b.String()
}
