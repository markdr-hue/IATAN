/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"fmt"
	"sort"
	"sync"

	"github.com/markdr-hue/IATAN/llm"
)

// Registry holds all registered tools keyed by name.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get retrieves a tool by name, returning an error if not found.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return t, nil
}

// sortedNames returns tool names in alphabetical order. Must be called with lock held.
func (r *Registry) sortedNames() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// List returns all registered tools in deterministic (alphabetical) order.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, name := range r.sortedNames() {
		out = append(out, r.tools[name])
	}
	return out
}

// Has returns true if a tool with the given name is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tools[name]
	return ok
}

// ToLLMTools converts every registered tool into the llm.ToolDef format
// expected by the LLM provider. Output is in deterministic (alphabetical) order.
func (r *Registry) ToLLMTools() []llm.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]llm.ToolDef, 0, len(r.tools))
	for _, name := range r.sortedNames() {
		t := r.tools[name]
		defs = append(defs, llm.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return defs
}

// ToLLMToolsFiltered returns tool definitions only for tools whose names
// are in the allowed set. Used to reduce token cost by sending fewer tools
// in modes that don't need the full set.
func (r *Registry) ToLLMToolsFiltered(allowed map[string]bool) []llm.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]llm.ToolDef, 0, len(allowed))
	for _, name := range r.sortedNames() {
		if allowed[name] {
			t := r.tools[name]
			defs = append(defs, llm.ToolDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			})
		}
	}
	return defs
}
