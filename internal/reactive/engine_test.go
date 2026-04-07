package reactive

import (
	"testing"
	"time"

	"moos/kernel/internal/graph"
)

// TestEvaluate_WatchReactGuard tests the full pipeline:
// A watcher observes MUTATE on knowledge_items, a guard checks that the
// source feed is active, and a reactor emits a status change rewrite.
func TestEvaluate_WatchReactGuard(t *testing.T) {
	now := time.Now()

	state := &graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			// A source feed that is active.
			"urn:moos:feed:yt.mlst": {
				URN:    "urn:moos:feed:yt.mlst",
				TypeID: "source_feed",
				Properties: map[string]graph.Property{
					"status": {Value: "active", Mutability: "mutable"},
					"name":   {Value: "MLST", Mutability: "immutable"},
				},
				CreatedAt: now,
			},
			// A knowledge item produced by that feed, initially status:raw.
			"urn:moos:ki:mlst-ep-42": {
				URN:    "urn:moos:ki:mlst-ep-42",
				TypeID: "knowledge_item",
				Properties: map[string]graph.Property{
					"status": {Value: "raw", Mutability: "mutable"},
					"title":  {Value: "MLST Episode 42", Mutability: "immutable"},
				},
				CreatedAt: now,
			},
			// Watcher: fires on MUTATE of knowledge_item nodes.
			"urn:moos:watcher:raw-ki-claim-extract": {
				URN:    "urn:moos:watcher:raw-ki-claim-extract",
				TypeID: "watcher",
				Properties: map[string]graph.Property{
					"name":               {Value: "raw-ki-claim-extract", Mutability: "immutable"},
					"match_rewrite_type":  {Value: "MUTATE", Mutability: "mutable"},
					"match_type_id":       {Value: "knowledge_item", Mutability: "mutable"},
					"match_urn_prefix":    {Value: "", Mutability: "mutable"},
					"match_port":          {Value: "", Mutability: "mutable"},
					"status":              {Value: "active", Mutability: "mutable"},
				},
				CreatedAt: now,
			},
			// Guard: check that the feed is active.
			"urn:moos:guard:feed-is-active": {
				URN:    "urn:moos:guard:feed-is-active",
				TypeID: "guard",
				Properties: map[string]graph.Property{
					"name":           {Value: "feed-is-active", Mutability: "immutable"},
					"predicate_type": {Value: "node_property", Mutability: "immutable"},
					"target_urn":     {Value: "urn:moos:feed:yt.mlst", Mutability: "mutable"},
					"field":          {Value: "status", Mutability: "mutable"},
					"expected_value": {Value: "active", Mutability: "mutable"},
					"negate":         {Value: false, Mutability: "mutable"},
				},
				CreatedAt: now,
			},
			// Reactor: emits a MUTATE to change KI status from raw → extracting.
			"urn:moos:reactor:emit-claim-extract-task": {
				URN:    "urn:moos:reactor:emit-claim-extract-task",
				TypeID: "reactor",
				Properties: map[string]graph.Property{
					"name":        {Value: "emit-claim-extract-task", Mutability: "immutable"},
					"action_type": {Value: "rewrite", Mutability: "immutable"},
					"status":      {Value: "active", Mutability: "mutable"},
					"template": {
						Value: map[string]any{
							"rewrite_type": "MUTATE",
							"actor":        "$actor",
							"target_urn":   "$matched_urn",
							"field":        "status",
							"new_value":    "extracting",
						},
						Mutability: "mutable",
					},
				},
				CreatedAt: now,
			},
		},
		Relations: map[graph.URN]graph.Relation{
			// Watcher → Reactor via WF17 triggers/triggered-by.
			"urn:moos:rel:watcher.triggers.reactor": {
				URN:             "urn:moos:rel:watcher.triggers.reactor",
				RewriteCategory: "WF17",
				SrcURN:          "urn:moos:watcher:raw-ki-claim-extract",
				SrcPort:         "triggers",
				TgtURN:          "urn:moos:reactor:emit-claim-extract-task",
				TgtPort:         "triggered-by",
				CreatedAt:       now,
			},
			// Guard → Watcher via WF17 guards/guarded-by.
			"urn:moos:rel:guard.guards.watcher": {
				URN:             "urn:moos:rel:guard.guards.watcher",
				RewriteCategory: "WF17",
				SrcURN:          "urn:moos:guard:feed-is-active",
				SrcPort:         "guards",
				TgtURN:          "urn:moos:watcher:raw-ki-claim-extract",
				TgtPort:         "guarded-by",
				CreatedAt:       now,
			},
			// Feed → KI via WF12 produces/produced-by.
			"urn:moos:rel:feed.produces.ki": {
				URN:             "urn:moos:rel:feed.produces.ki",
				RewriteCategory: "WF12",
				SrcURN:          "urn:moos:feed:yt.mlst",
				SrcPort:         "produces",
				TgtURN:          "urn:moos:ki:mlst-ep-42",
				TgtPort:         "produced-by",
				CreatedAt:       now,
			},
		},
	}

	engine := Engine{State: state}

	// Simulate a MUTATE rewrite that changes the KI status (e.g., from raw to raw).
	// The watcher matches on MUTATE + knowledge_item, regardless of field value.
	rewrite := graph.PersistedRewrite{
		Envelope: graph.Envelope{
			RewriteType: graph.MUTATE,
			Actor:       "urn:moos:user:sam",
			TargetURN:   "urn:moos:ki:mlst-ep-42",
			Field:       "status",
			NewValue:    "raw",
		},
		AppliedAt: now,
		LogSeq:    100,
	}

	proposals := engine.Evaluate(rewrite)

	if len(proposals) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(proposals))
	}

	p := proposals[0]
	if p.RewriteType != graph.MUTATE {
		t.Errorf("expected MUTATE, got %s", p.RewriteType)
	}
	if p.TargetURN != "urn:moos:ki:mlst-ep-42" {
		t.Errorf("expected target urn:moos:ki:mlst-ep-42, got %s", p.TargetURN)
	}
	if p.Field != "status" {
		t.Errorf("expected field 'status', got %s", p.Field)
	}
	if p.NewValue != "extracting" {
		t.Errorf("expected new_value 'extracting', got %v", p.NewValue)
	}
	if p.Actor != "urn:moos:user:sam" {
		t.Errorf("expected actor urn:moos:user:sam, got %s", p.Actor)
	}
}

// TestEvaluate_GuardFails verifies the reactor does NOT fire when a guard fails.
func TestEvaluate_GuardFails(t *testing.T) {
	now := time.Now()

	state := &graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			// Feed is PAUSED — guard should fail.
			"urn:moos:feed:yt.mlst": {
				URN:    "urn:moos:feed:yt.mlst",
				TypeID: "source_feed",
				Properties: map[string]graph.Property{
					"status": {Value: "paused", Mutability: "mutable"},
				},
			},
			"urn:moos:ki:mlst-ep-42": {
				URN:    "urn:moos:ki:mlst-ep-42",
				TypeID: "knowledge_item",
				Properties: map[string]graph.Property{
					"status": {Value: "raw", Mutability: "mutable"},
				},
			},
			"urn:moos:watcher:raw-ki-claim-extract": {
				URN:    "urn:moos:watcher:raw-ki-claim-extract",
				TypeID: "watcher",
				Properties: map[string]graph.Property{
					"match_rewrite_type": {Value: "MUTATE", Mutability: "mutable"},
					"match_type_id":      {Value: "knowledge_item", Mutability: "mutable"},
					"status":             {Value: "active", Mutability: "mutable"},
				},
			},
			"urn:moos:guard:feed-is-active": {
				URN:    "urn:moos:guard:feed-is-active",
				TypeID: "guard",
				Properties: map[string]graph.Property{
					"predicate_type": {Value: "node_property", Mutability: "immutable"},
					"target_urn":     {Value: "urn:moos:feed:yt.mlst", Mutability: "mutable"},
					"field":          {Value: "status", Mutability: "mutable"},
					"expected_value": {Value: "active", Mutability: "mutable"},
					"negate":         {Value: false, Mutability: "mutable"},
				},
			},
			"urn:moos:reactor:emit-claim-extract-task": {
				URN:    "urn:moos:reactor:emit-claim-extract-task",
				TypeID: "reactor",
				Properties: map[string]graph.Property{
					"action_type": {Value: "rewrite", Mutability: "immutable"},
					"status":      {Value: "active", Mutability: "mutable"},
					"template": {
						Value: map[string]any{
							"rewrite_type": "MUTATE",
							"actor":        "$actor",
							"target_urn":   "$matched_urn",
							"field":        "status",
							"new_value":    "extracting",
						},
						Mutability: "mutable",
					},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{
			"urn:moos:rel:w.triggers.r": {
				URN:     "urn:moos:rel:w.triggers.r",
				SrcURN:  "urn:moos:watcher:raw-ki-claim-extract",
				SrcPort: "triggers",
				TgtURN:  "urn:moos:reactor:emit-claim-extract-task",
				TgtPort: "triggered-by",
			},
			"urn:moos:rel:g.guards.w": {
				URN:     "urn:moos:rel:g.guards.w",
				SrcURN:  "urn:moos:guard:feed-is-active",
				SrcPort: "guards",
				TgtURN:  "urn:moos:watcher:raw-ki-claim-extract",
				TgtPort: "guarded-by",
			},
		},
	}

	engine := Engine{State: state}

	rewrite := graph.PersistedRewrite{
		Envelope: graph.Envelope{
			RewriteType: graph.MUTATE,
			Actor:       "urn:moos:user:sam",
			TargetURN:   "urn:moos:ki:mlst-ep-42",
			Field:       "status",
			NewValue:    "raw",
		},
		AppliedAt: now,
		LogSeq:    100,
	}

	proposals := engine.Evaluate(rewrite)

	if len(proposals) != 0 {
		t.Fatalf("expected 0 proposals (guard should fail), got %d", len(proposals))
	}
}

// TestEvaluate_PausedWatcher verifies paused watchers are skipped.
func TestEvaluate_PausedWatcher(t *testing.T) {
	state := &graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:ki:test": {
				URN:        "urn:moos:ki:test",
				TypeID:     "knowledge_item",
				Properties: map[string]graph.Property{"status": {Value: "raw"}},
			},
			"urn:moos:watcher:paused": {
				URN:    "urn:moos:watcher:paused",
				TypeID: "watcher",
				Properties: map[string]graph.Property{
					"match_rewrite_type": {Value: "MUTATE"},
					"match_type_id":      {Value: "knowledge_item"},
					"status":             {Value: "paused"}, // paused!
				},
			},
			"urn:moos:reactor:test": {
				URN:    "urn:moos:reactor:test",
				TypeID: "reactor",
				Properties: map[string]graph.Property{
					"action_type": {Value: "rewrite"},
					"status":      {Value: "active"},
					"template":    {Value: map[string]any{"rewrite_type": "MUTATE", "target_urn": "$matched_urn", "field": "status", "new_value": "done", "actor": "$actor"}},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{
			"urn:moos:rel:w.t.r": {
				URN: "urn:moos:rel:w.t.r", SrcURN: "urn:moos:watcher:paused", SrcPort: "triggers",
				TgtURN: "urn:moos:reactor:test", TgtPort: "triggered-by",
			},
		},
	}

	engine := Engine{State: state}
	proposals := engine.Evaluate(graph.PersistedRewrite{
		Envelope: graph.Envelope{RewriteType: graph.MUTATE, Actor: "urn:moos:user:sam", TargetURN: "urn:moos:ki:test", Field: "status", NewValue: "raw"},
	})

	if len(proposals) != 0 {
		t.Fatalf("expected 0 proposals (watcher paused), got %d", len(proposals))
	}
}

// TestEvaluate_NegatedGuard verifies that negate=true inverts the predicate.
func TestEvaluate_NegatedGuard(t *testing.T) {
	now := time.Now()

	state := &graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:ki:test": {
				URN: "urn:moos:ki:test", TypeID: "knowledge_item",
				Properties: map[string]graph.Property{"status": {Value: "raw"}},
			},
			"urn:moos:watcher:test": {
				URN: "urn:moos:watcher:test", TypeID: "watcher",
				Properties: map[string]graph.Property{
					"match_rewrite_type": {Value: "MUTATE"},
					"match_type_id":      {Value: "knowledge_item"},
					"status":             {Value: "active"},
				},
			},
			// Guard with negate=true: passes when node does NOT exist.
			"urn:moos:guard:no-duplicate": {
				URN: "urn:moos:guard:no-duplicate", TypeID: "guard",
				Properties: map[string]graph.Property{
					"predicate_type": {Value: "node_exists"},
					"target_urn":     {Value: "urn:moos:claim:nonexistent"},
					"negate":         {Value: true}, // passes because node doesn't exist
				},
			},
			"urn:moos:reactor:test": {
				URN: "urn:moos:reactor:test", TypeID: "reactor",
				Properties: map[string]graph.Property{
					"action_type": {Value: "rewrite"},
					"status":      {Value: "active"},
					"template":    {Value: map[string]any{"rewrite_type": "MUTATE", "actor": "$actor", "target_urn": "$matched_urn", "field": "status", "new_value": "done"}},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{
			"urn:moos:rel:w.t.r": {
				URN: "urn:moos:rel:w.t.r", SrcURN: "urn:moos:watcher:test", SrcPort: "triggers",
				TgtURN: "urn:moos:reactor:test", TgtPort: "triggered-by",
			},
			"urn:moos:rel:g.g.w": {
				URN: "urn:moos:rel:g.g.w", SrcURN: "urn:moos:guard:no-duplicate", SrcPort: "guards",
				TgtURN: "urn:moos:watcher:test", TgtPort: "guarded-by",
			},
		},
	}

	engine := Engine{State: state}
	proposals := engine.Evaluate(graph.PersistedRewrite{
		Envelope:  graph.Envelope{RewriteType: graph.MUTATE, Actor: "urn:moos:user:sam", TargetURN: "urn:moos:ki:test", Field: "status", NewValue: "raw"},
		AppliedAt: now,
	})

	if len(proposals) != 1 {
		t.Fatalf("expected 1 proposal (negated guard passes), got %d", len(proposals))
	}
}

// TestEvaluate_NoMatch verifies that non-matching rewrites produce no proposals.
func TestEvaluate_NoMatch(t *testing.T) {
	state := &graph.GraphState{
		Nodes: map[graph.URN]graph.Node{
			"urn:moos:user:sam": {
				URN: "urn:moos:user:sam", TypeID: "user",
				Properties: map[string]graph.Property{"name": {Value: "sam"}},
			},
			"urn:moos:watcher:test": {
				URN: "urn:moos:watcher:test", TypeID: "watcher",
				Properties: map[string]graph.Property{
					"match_rewrite_type": {Value: "MUTATE"},
					"match_type_id":      {Value: "knowledge_item"}, // only matches KIs
					"status":             {Value: "active"},
				},
			},
		},
		Relations: map[graph.URN]graph.Relation{},
	}

	engine := Engine{State: state}

	// ADD a user node — watcher only matches MUTATE on knowledge_items.
	proposals := engine.Evaluate(graph.PersistedRewrite{
		Envelope: graph.Envelope{RewriteType: graph.ADD, Actor: "urn:moos:user:sam", NodeURN: "urn:moos:user:bob", TypeID: "user"},
	})

	if len(proposals) != 0 {
		t.Fatalf("expected 0 proposals (no match), got %d", len(proposals))
	}
}
