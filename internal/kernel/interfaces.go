package kernel

import "moos/kernel/internal/graph"

// InspectKernel provides read-only access to kernel state.
type InspectKernel interface {
	State() graph.GraphState
	Node(urn graph.URN) (graph.Node, bool)
	Nodes() []graph.Node
	Relation(urn graph.URN) (graph.Relation, bool)
	Relations() []graph.Relation
	RelationsSrc(urn graph.URN) []graph.Relation
	RelationsTgt(urn graph.URN) []graph.Relation
	Log() []graph.PersistedRewrite
	LogLen() int
}

// WriteKernel provides write access to the kernel.
type WriteKernel interface {
	Apply(env graph.Envelope) (graph.EvalResult, error)
	ApplyProgram(envelopes []graph.Envelope) ([]graph.EvalResult, error)
	SeedIfAbsent(env graph.Envelope) error
}

// ObservableKernel provides pub/sub access to the rewrite event stream.
type ObservableKernel interface {
	Subscribe(id string) <-chan graph.PersistedRewrite
	Unsubscribe(id string)
}

// Compile-time assertions that Runtime satisfies all three interfaces.
var (
	_ InspectKernel    = (*Runtime)(nil)
	_ WriteKernel      = (*Runtime)(nil)
	_ ObservableKernel = (*Runtime)(nil)
)
