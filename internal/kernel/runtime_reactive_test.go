package kernel

import (
	"strings"
	"testing"
	"time"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/hdc"
)

// TestRuntime_ReactiveChain verifies that Apply triggers the reactive engine
// and applies reactor proposals when a matching watcher+reactor is active.
func TestRuntime_ReactiveChain(t *testing.T) {
	// nil registry: validate() returns nil for all calls — pure structural test.
	rt := &Runtime{
		state:       graph.NewGraphState(),
		store:       NewMemStore(),
		registry:    nil,
		subscribers: make(map[string]chan graph.PersistedRewrite),
	}

	now := time.Now().UTC()

	// Seed the graph directly via SeedIfAbsent to set up watcher, reactor, KI.
	seeds := []graph.Envelope{
		// A knowledge_item to be triggered on.
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:kernel",
			NodeURN:     "urn:moos:ki:test-item",
			TypeID:      "knowledge_item",
			Properties: map[string]graph.Property{
				"status": {Value: "raw", Mutability: "mutable"},
				"title":  {Value: "Test KI", Mutability: "immutable"},
			},
		},
		// Watcher: fires on MUTATE of knowledge_item.
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:kernel",
			NodeURN:     "urn:moos:watcher:test-watch",
			TypeID:      "watcher",
			Properties: map[string]graph.Property{
				"name":               {Value: "test-watch", Mutability: "immutable"},
				"created_at":         {Value: now.Format(time.RFC3339), Mutability: "immutable"},
				"match_rewrite_type": {Value: "MUTATE", Mutability: "mutable"},
				"match_type_id":      {Value: "knowledge_item", Mutability: "mutable"},
				"status":             {Value: "active", Mutability: "mutable"},
			},
		},
		// Reactor: emits MUTATE → status = "claim-pending".
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:kernel",
			NodeURN:     "urn:moos:reactor:test-react",
			TypeID:      "reactor",
			Properties: map[string]graph.Property{
				"name":        {Value: "test-react", Mutability: "immutable"},
				"created_at":  {Value: now.Format(time.RFC3339), Mutability: "immutable"},
				"action_type": {Value: "rewrite", Mutability: "immutable"},
				"status":      {Value: "active", Mutability: "mutable"},
				"template": {
					Value: map[string]any{
						"rewrite_type": "MUTATE",
						"actor":        "$actor",
						"target_urn":   "$matched_urn",
						"field":        "status",
						"new_value":    "claim-pending",
					},
					Mutability: "mutable",
				},
			},
		},
		// LINK: watcher triggers reactor (WF17).
		{
			RewriteType:     graph.LINK,
			Actor:           "urn:moos:kernel",
			RelationURN:     "urn:moos:rel:watch.triggers.react",
			RewriteCategory: "WF17",
			SrcURN:          "urn:moos:watcher:test-watch",
			SrcPort:         "triggers",
			TgtURN:          "urn:moos:reactor:test-react",
			TgtPort:         "triggered-by",
		},
	}
	for _, env := range seeds {
		if err := rt.SeedIfAbsent(env); err != nil {
			t.Fatalf("seed failed: %v", err)
		}
	}

	logBefore := rt.LogLen()

	// Fire the MUTATE — should trigger watcher → reactor → reactive MUTATE.
	_, err := rt.Apply(graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:ki:test-item",
		Field:       "status",
		NewValue:    "raw",
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	logAfter := rt.LogLen()
	if logAfter != logBefore+2 {
		t.Errorf("expected log to grow by 2 (trigger + reactive), got %d → %d", logBefore, logAfter)
	}

	// Verify the KI status was changed by the reactor.
	ki, ok := rt.Node("urn:moos:ki:test-item")
	if !ok {
		t.Fatal("KI not found after apply")
	}
	status, _ := ki.Properties["status"].Value.(string)
	if status != "claim-pending" {
		t.Errorf("expected KI status=claim-pending, got %q", status)
	}
}

// TestRuntime_THookFires verifies that a t_hook node owned by an affected node
// fires its react_template when a matching MUTATE is applied (M6).
func TestRuntime_THookFires(t *testing.T) {
	rt := &Runtime{
		state:       graph.NewGraphState(),
		store:       NewMemStore(),
		registry:    nil,
		subscribers: make(map[string]chan graph.PersistedRewrite),
	}

	now := time.Now().UTC()

	seeds := []graph.Envelope{
		// Target node that the t_hook watches.
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:kernel",
			NodeURN:     "urn:moos:ki:hook-target",
			TypeID:      "knowledge_item",
			Properties: map[string]graph.Property{
				"status": {Value: "raw", Mutability: "mutable"},
				"title":  {Value: "Hook Target", Mutability: "immutable"},
			},
		},
		// T-hook: fires on any MUTATE of the owner node.
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:kernel",
			NodeURN:     "urn:moos:t_hook:hook-target.on-mutate",
			TypeID:      "t_hook",
			Properties: map[string]graph.Property{
				"owner_urn":  {Value: "urn:moos:ki:hook-target", Mutability: "immutable"},
				"status":     {Value: "active", Mutability: "mutable"},
				"created_at": {Value: now.Format(time.RFC3339), Mutability: "immutable"},
				"event_shape": {
					Value:      map[string]any{"rewrite_type": "MUTATE"},
					Mutability: "mutable",
				},
				"react_template": {
					Value: map[string]any{
						"rewrite_type": "MUTATE",
						"actor":        "$actor",
						"target_urn":   "$matched_urn",
						"field":        "status",
						"new_value":    "hook-fired",
					},
					Mutability: "mutable",
				},
			},
		},
	}
	for _, env := range seeds {
		if err := rt.SeedIfAbsent(env); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	logBefore := rt.LogLen()

	// Trigger: MUTATE the owner node — t_hook should fire and set status="hook-fired".
	_, err := rt.Apply(graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:ki:hook-target",
		Field:       "status",
		NewValue:    "intermediate",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Log should grow by 2: the trigger MUTATE + the t_hook's reactive MUTATE.
	if got := rt.LogLen(); got != logBefore+2 {
		t.Errorf("expected log +2 (trigger + t_hook proposal), got %d → %d", logBefore, got)
	}

	ki, ok := rt.Node("urn:moos:ki:hook-target")
	if !ok {
		t.Fatal("KI not found after Apply")
	}
	status, _ := ki.Properties["status"].Value.(string)
	if status != "hook-fired" {
		t.Errorf("expected status=hook-fired after t_hook fires, got %q", status)
	}
}

// TestRuntime_THookEventShapeFilter verifies that a t_hook with an event_shape
// filter does NOT fire on non-matching rewrites (LINK ≠ MUTATE).
func TestRuntime_THookEventShapeFilter(t *testing.T) {
	rt := &Runtime{
		state:       graph.NewGraphState(),
		store:       NewMemStore(),
		registry:    nil,
		subscribers: make(map[string]chan graph.PersistedRewrite),
	}

	now := time.Now().UTC()

	seeds := []graph.Envelope{
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:kernel",
			NodeURN:     "urn:moos:ki:filter-target",
			TypeID:      "knowledge_item",
			Properties: map[string]graph.Property{
				"status": {Value: "raw", Mutability: "mutable"},
				"title":  {Value: "Filter Target", Mutability: "immutable"},
			},
		},
		// T-hook: fires ONLY on LINK rewrites (not MUTATE).
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:kernel",
			NodeURN:     "urn:moos:t_hook:filter-target.on-link",
			TypeID:      "t_hook",
			Properties: map[string]graph.Property{
				"owner_urn":  {Value: "urn:moos:ki:filter-target", Mutability: "immutable"},
				"status":     {Value: "active", Mutability: "mutable"},
				"created_at": {Value: now.Format(time.RFC3339), Mutability: "immutable"},
				"event_shape": {
					Value:      map[string]any{"rewrite_type": "LINK"},
					Mutability: "mutable",
				},
				"react_template": {
					Value: map[string]any{
						"rewrite_type": "MUTATE",
						"actor":        "$actor",
						"target_urn":   "$matched_urn",
						"field":        "status",
						"new_value":    "should-not-be-set",
					},
					Mutability: "mutable",
				},
			},
		},
	}
	for _, env := range seeds {
		if err := rt.SeedIfAbsent(env); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	logBefore := rt.LogLen()

	// A MUTATE — the t_hook wants LINK so it should NOT fire.
	_, err := rt.Apply(graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:ki:filter-target",
		Field:       "status",
		NewValue:    "updated",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Log should grow by only 1 (no t_hook fire).
	if got := rt.LogLen(); got != logBefore+1 {
		t.Errorf("expected log +1 (trigger only, t_hook filtered), got %d → %d", logBefore, got)
	}

	ki, ok := rt.Node("urn:moos:ki:filter-target")
	if !ok {
		t.Fatal("KI not found")
	}
	status, _ := ki.Properties["status"].Value.(string)
	if status == "should-not-be-set" {
		t.Error("t_hook fired when it should have been filtered by event_shape")
	}
	if status != "updated" {
		t.Errorf("expected status=updated, got %q", status)
	}
}

// TestRuntime_GateBlocksRewrite verifies that a gate node linked to a target via
// "guards"/"guarded-by" blocks a MUTATE when its predicate fails (M8 fail-closed).
func TestRuntime_GateBlocksRewrite(t *testing.T) {
	rt := &Runtime{
		state:       graph.NewGraphState(),
		store:       NewMemStore(),
		registry:    nil,
		subscribers: make(map[string]chan graph.PersistedRewrite),
	}

	now := time.Now().UTC()

	seeds := []graph.Envelope{
		// Node to be gated.
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:kernel",
			NodeURN:     "urn:moos:ki:gated-item",
			TypeID:      "knowledge_item",
			Properties: map[string]graph.Property{
				"title":  {Value: "Gated KI", Mutability: "immutable"},
				"status": {Value: "raw", Mutability: "mutable"},
				// "approval" field intentionally absent — gate will check for it.
			},
		},
		// Gate: blocks rewrites on gated-item until a separate clearance node exists.
		// The predicate checks a DIFFERENT node — this avoids circular self-reference.
		// M8 semantics: "this data is incomplete until external approval is recorded."
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:kernel",
			NodeURN:     "urn:moos:gate:require-approval",
			TypeID:      "gate",
			Properties: map[string]graph.Property{
				"name":           {Value: "require-approval", Mutability: "immutable"},
				"created_at":     {Value: now.Format(time.RFC3339), Mutability: "immutable"},
				"predicate_type": {Value: "node_exists", Mutability: "mutable"},
				"target_urn":     {Value: "urn:moos:claim:gated-item.approval", Mutability: "mutable"},
				"negate":         {Value: false, Mutability: "mutable"},
			},
		},
		// LINK: gate guards the gated node.
		{
			RewriteType:     graph.LINK,
			Actor:           "urn:moos:kernel",
			RelationURN:     "urn:moos:rel:gate.guards.ki",
			RewriteCategory: "WF17",
			SrcURN:          "urn:moos:gate:require-approval",
			SrcPort:         "guards",
			TgtURN:          "urn:moos:ki:gated-item",
			TgtPort:         "guarded-by",
		},
	}
	for _, env := range seeds {
		if err := rt.SeedIfAbsent(env); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Attempt MUTATE while gate predicate fails (approval field absent) — must be blocked.
	_, err := rt.Apply(graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:ki:gated-item",
		Field:       "status",
		NewValue:    "should-not-land",
	})
	if err == nil {
		t.Fatal("expected gate to block the MUTATE, but Apply succeeded")
	}
	if !strings.Contains(err.Error(), "gate(M8)") {
		t.Errorf("expected gate error, got: %v", err)
	}

	// Satisfy the gate: ADD the external clearance node that the predicate checks.
	if _, err := rt.Apply(graph.Envelope{
		RewriteType: graph.ADD,
		Actor:       "urn:moos:user:sam",
		NodeURN:     "urn:moos:claim:gated-item.approval",
		TypeID:      "claim",
		Properties: map[string]graph.Property{
			"text":       {Value: "approved by sam", Mutability: "immutable"},
			"created_at": {Value: now.Format(time.RFC3339), Mutability: "immutable"},
		},
	}); err != nil {
		t.Fatalf("ADD clearance node: %v", err)
	}

	// Now the gate predicate passes — MUTATE on status should succeed.
	if _, err := rt.Apply(graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:user:sam",
		TargetURN:   "urn:moos:ki:gated-item",
		Field:       "status",
		NewValue:    "approved",
	}); err != nil {
		t.Fatalf("MUTATE after gate satisfied: %v", err)
	}

	ki, ok := rt.Node("urn:moos:ki:gated-item")
	if !ok {
		t.Fatal("KI not found")
	}
	status, _ := ki.Properties["status"].Value.(string)
	if status != "approved" {
		t.Errorf("expected status=approved after gate satisfied, got %q", status)
	}
}

func TestRuntime_HDCDriftEmitsClaimAndAnnotation(t *testing.T) {
	rt := &Runtime{
		state:       graph.NewGraphState(),
		store:       NewMemStore(),
		registry:    nil,
		hdcIndex:    hdc.NewLiveIndex(1.1),
		subscribers: make(map[string]chan graph.PersistedRewrite),
	}

	adds := []graph.Envelope{
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:kernel:test",
			NodeURN:     "urn:moos:ki:a",
			TypeID:      "knowledge_item",
			Properties: map[string]graph.Property{
				"title":  {Value: "Alpha", Mutability: "immutable"},
				"status": {Value: "raw", Mutability: "mutable"},
			},
		},
		{
			RewriteType: graph.ADD,
			Actor:       "urn:moos:kernel:test",
			NodeURN:     "urn:moos:ki:b",
			TypeID:      "knowledge_item",
			Properties: map[string]graph.Property{
				"title":  {Value: "Beta", Mutability: "immutable"},
				"status": {Value: "raw", Mutability: "mutable"},
			},
		},
	}

	for _, env := range adds {
		if _, err := rt.Apply(env); err != nil {
			t.Fatalf("apply add failed: %v", err)
		}
	}

	expressions := rt.HDCTypeExpressions()
	var maxDrift float64
	for _, row := range expressions {
		if row.DeclaredType == "claim" {
			continue
		}
		if row.Drift > maxDrift {
			maxDrift = row.Drift
		}
	}
	if maxDrift <= 0 {
		t.Fatalf("expected positive drift score from type-expression index, got %f", maxDrift)
	}

	rt.mu.Lock()
	rt.hdcIndex = hdc.NewLiveIndex(maxDrift * 0.5)
	rt.hdcIndex.Recompute(rt.state, nil)
	rt.mu.Unlock()

	if _, err := rt.Apply(graph.Envelope{
		RewriteType: graph.MUTATE,
		Actor:       "urn:moos:kernel:test",
		TargetURN:   "urn:moos:ki:a",
		Field:       "status",
		NewValue:    "claim-pending",
	}); err != nil {
		t.Fatalf("apply mutate trigger failed: %v", err)
	}

	var claimCount int
	for _, n := range rt.state.Nodes {
		if n.TypeID == "claim" {
			claimCount++
			if _, ok := n.Properties["subject_urn"]; !ok {
				t.Fatal("drift claim missing subject_urn")
			}
		}
	}
	if claimCount == 0 {
		t.Fatal("expected at least one drift claim node")
	}

	var annotationCount int
	for _, rel := range rt.state.Relations {
		if rel.RewriteCategory == graph.WF11 {
			annotationCount++
		}
	}
	if annotationCount == 0 {
		t.Fatal("expected at least one WF11 drift annotation relation")
	}

	expressions = rt.HDCTypeExpressions()
	if len(expressions) == 0 {
		t.Fatal("expected non-empty HDC type-expression index")
	}
}
