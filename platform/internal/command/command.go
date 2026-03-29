package command

import "strings"

// Result holds the resolved command type and its parameters.
type Result struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

// Matcher determines whether a natural language instruction maps to a specific command.
type Matcher interface {
	Match(instruction string, original string) (*Result, bool)
}

// KeywordMatcher matches if any keyword appears in the lowercased instruction.
type KeywordMatcher struct {
	Keywords []string
	CmdType  string
	ParamsFn func(instruction, original string) map[string]any
}

func (m *KeywordMatcher) Match(instruction string, original string) (*Result, bool) {
	for _, kw := range m.Keywords {
		if strings.Contains(instruction, kw) {
			params := map[string]any{"instruction": original}
			if m.ParamsFn != nil {
				params = m.ParamsFn(instruction, original)
			}
			return &Result{Type: m.CmdType, Params: params}, true
		}
	}
	return nil, false
}

// Registry holds an ordered list of matchers. First match wins.
type Registry struct {
	matchers []Matcher
}

// NewRegistry creates an empty command registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a matcher. Matchers are evaluated in registration order.
func (r *Registry) Register(m Matcher) {
	r.matchers = append(r.matchers, m)
}

// Resolve finds the first matching command for the instruction.
func (r *Registry) Resolve(instruction string) Result {
	lower := strings.ToLower(instruction)
	for _, m := range r.matchers {
		if result, ok := m.Match(lower, instruction); ok {
			return *result
		}
	}
	return Result{Type: "semantic", Params: map[string]any{"instruction": instruction}}
}

// DefaultRegistry returns a registry pre-loaded with all built-in command matchers.
func DefaultRegistry() *Registry {
	r := NewRegistry()

	r.Register(&KeywordMatcher{
		Keywords: []string{"stop", "halt"},
		CmdType:  "stop",
		ParamsFn: func(instr, _ string) map[string]any {
			return map[string]any{"emergency": strings.Contains(instr, "emergency")}
		},
	})
	r.Register(&KeywordMatcher{Keywords: []string{"dance"}, CmdType: "dance"})
	r.Register(&KeywordMatcher{Keywords: []string{"wave", "hello", "greet"}, CmdType: "wave"})
	r.Register(&KeywordMatcher{Keywords: []string{"bow", "respect"}, CmdType: "bow"})
	r.Register(&KeywordMatcher{Keywords: []string{"sit down", "sit ", "crouch"}, CmdType: "sit"})
	r.Register(&KeywordMatcher{Keywords: []string{"jump", "hop"}, CmdType: "jump"})
	r.Register(&KeywordMatcher{Keywords: []string{"look", "scan", "search"}, CmdType: "look_around"})
	r.Register(&KeywordMatcher{Keywords: []string{"stretch", "warm"}, CmdType: "stretch"})
	r.Register(&KeywordMatcher{
		Keywords: []string{"forward", "ahead"},
		CmdType:  "move_relative",
		ParamsFn: func(_, _ string) map[string]any {
			return map[string]any{"direction": "forward", "distance": 1.0}
		},
	})
	r.Register(&KeywordMatcher{
		Keywords: []string{"back"},
		CmdType:  "move_relative",
		ParamsFn: func(_, _ string) map[string]any {
			return map[string]any{"direction": "backward", "distance": 1.0}
		},
	})
	r.Register(&KeywordMatcher{
		Keywords: []string{"go to", "move to"},
		CmdType:  "move",
		ParamsFn: func(_, _ string) map[string]any {
			return map[string]any{"x": 0.0, "y": 0.0}
		},
	})

	return r
}
