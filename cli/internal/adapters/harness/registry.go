package harness

// Registry holds all known Harness adapters and provides lookup,
// detection, and multi-dispatch capabilities.
type Registry struct {
	adapters map[string]Adapter
	order    []string // preserves registration order
	root     string
}

// NewRegistry creates a Registry for the given project root and
// auto-registers all known adapters.
func NewRegistry(projectRoot string) *Registry {
	r := &Registry{
		adapters: make(map[string]Adapter),
		root:     projectRoot,
	}
	r.Register(NewClaudeAdapter(projectRoot))
	r.Register(NewCodexAdapter(projectRoot))
	r.Register(NewWindsurfAdapter(projectRoot))
	r.Register(NewPiAdapter(projectRoot))
	return r
}

// Register adds an adapter to the registry.
func (r *Registry) Register(adapter Adapter) {
	name := adapter.Name()
	r.adapters[name] = adapter
	r.order = append(r.order, name)
}

// Get returns the adapter for the given Harness name.
func (r *Registry) Get(name string) (Adapter, bool) {
	a, ok := r.adapters[name]
	return a, ok
}

// DetectAll returns adapters whose Harness is detected in the project.
func (r *Registry) DetectAll() []Adapter {
	var detected []Adapter
	for _, name := range r.order {
		a := r.adapters[name]
		if a.DetectHarness() {
			detected = append(detected, a)
		}
	}
	return detected
}

// All returns all registered adapters in registration order.
func (r *Registry) All() []Adapter {
	all := make([]Adapter, 0, len(r.order))
	for _, name := range r.order {
		all = append(all, r.adapters[name])
	}
	return all
}
