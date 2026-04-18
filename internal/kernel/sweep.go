package kernel

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/reactive"
)

// ----------------------------------------------------------------------------
// Time-driven sweep (§M14 / §M15 — hook-predicates sub-program)
// ----------------------------------------------------------------------------
//
// The sweep walks all pending t_hook nodes on every tick and emits a
// governance_proposal per hook whose predicate evaluates true. Proposals
// sit at status=pending awaiting human or admin-session ratification —
// the sweep NEVER auto-applies a hook's react_template.
//
// Firing semantics (per T=168 round-9 plan, sam's direction):
//   Propose via WF13 governance. Sweep ADDs a governance_proposal carrying
//   the source hook URN, the calendar-T it fired at, and a snapshot of
//   the hook's react_template. An admin session reviews, MUTATEs
//   governance_proposal.status from pending to approved or rejected, and
//   a separate reactor (not in this PR) applies the proposed envelope
//   when status becomes approved.
//
// Idempotency:
//   The sweep uses the existing-proposal set as the idempotency key. On
//   every tick, it builds a lookup of hooks that already have a proposal
//   (matching by governance_proposal.source_t_hook_urn) and skips those
//   hooks in the main loop. This avoids needing a firing_state property
//   on t_hook (which is not in the v3.9 type spec — additive MUTATE would
//   fail). When firing_state lands in a later ontology bump, the sweep
//   can switch to that mechanism.
//
// Kernel does not track calendar-T itself (§M13); the caller supplies it.
// RunTimedSweep derives T from wall-clock via CurrentSweepTDay.

// sweepTDay0 is T=0 as UTC: 2025-11-01 00:00 CEST = 2025-10-31 23:00 UTC.
// Matches transport/server.go tDay0 so both derive the same T-day.
var sweepTDay0 = time.Date(2025, 10, 31, 23, 0, 0, 0, time.UTC)

// CurrentSweepTDay returns the current calendar T-day (wall-clock derived).
// Exported so callers that want to trigger a manual sweep without the
// goroutine can pass the same T to SweepOnce.
func CurrentSweepTDay() int {
	return int(time.Now().UTC().Sub(sweepTDay0).Hours() / 24)
}

// DefaultSweepActor is the actor URN used by the sweep when ADDing
// governance_proposal nodes. Override via Runtime.SetSweepActor before
// starting the sweep goroutine if a more specific kernel URN is known
// (e.g. urn:moos:kernel:hp-laptop.primary).
const DefaultSweepActor graph.URN = "urn:moos:kernel:sweep"

// SweepOnce inspects the given state and returns the list of
// governance_proposal ADD envelopes for every pending t_hook whose
// predicate evaluates true at currentT.
//
// Pure: does not mutate state or perform IO. The caller (typically
// Runtime.sweepOnce) is responsible for applying the returned envelopes
// atomically via ApplyProgram.
//
// Idempotency: hooks that already have a matching governance_proposal
// (by source_t_hook_urn equality) are skipped.
//
// baseLogSeq is used only for URN disambiguation across hooks within a
// single tick — it's appended to the URN suffix so two hooks firing in
// the same tick get distinct proposal URNs. The actual log sequencing
// happens at Apply time.
func SweepOnce(state graph.GraphState, currentT int, actor graph.URN, baseLogSeq int64) []graph.Envelope {
	// Build the idempotency set: hooks that already have a proposal.
	// Single O(nodes) pass before the main loop.
	proposedHooks := make(map[graph.URN]struct{})
	for _, n := range state.Nodes {
		if n.TypeID != "governance_proposal" {
			continue
		}
		p, ok := n.Properties["source_t_hook_urn"]
		if !ok {
			continue
		}
		s, _ := p.Value.(string)
		if s != "" {
			proposedHooks[graph.URN(s)] = struct{}{}
		}
	}

	var envelopes []graph.Envelope
	emitted := int64(0)

	for _, n := range state.Nodes {
		if n.TypeID != "t_hook" {
			continue
		}
		// Idempotency check: already proposed this hook → skip.
		if _, already := proposedHooks[n.URN]; already {
			continue
		}
		// Read the predicate. Missing or nil → skip.
		predProp, hasPred := n.Properties["predicate"]
		if !hasPred || predProp.Value == nil {
			continue
		}
		// Evaluate. EvaluateThookPredicate handles the json round-trip
		// when the value arrives from log replay as map[string]any or
		// any other shape; returns false on malformed / unknown kinds.
		stateCopy := state // SweepOnce doesn't mutate; pass addr-of-local.
		if !reactive.EvaluateThookPredicate(predProp.Value, &stateCopy, currentT) {
			continue
		}

		// Compose the proposal envelope. The URN encodes the source hook
		// slug + calendar-T + sequence offset so operators can trace it.
		hookSlug := slugFromURN(n.URN)
		proposalURN := graph.URN(fmt.Sprintf("urn:moos:proposal:kernel.%s-t%d-seq%d", hookSlug, currentT, baseLogSeq+emitted))
		emitted++

		// Carry the react_template verbatim so an approver can see
		// exactly what would apply if they ratified the proposal.
		var reactTemplate any
		if p, ok := n.Properties["react_template"]; ok {
			reactTemplate = p.Value
		}
		// Owner is the natural breadcrumb back to the program that
		// owns the firing hook.
		var ownerURN any
		if p, ok := n.Properties["owner_urn"]; ok {
			ownerURN = p.Value
		}

		now := time.Now().UTC().Format(time.RFC3339)
		title := fmt.Sprintf("Fire t_hook %s at T=%d", n.URN, currentT)

		envelopes = append(envelopes, graph.Envelope{
			RewriteType: graph.ADD,
			Actor:       actor,
			NodeURN:     proposalURN,
			TypeID:      "governance_proposal",
			Properties: map[string]graph.Property{
				// Required immutable property per v3.9 ontology.
				"title": {Value: title, Mutability: "immutable"},
				// Mutable-with-principal-scope per spec; approver flips this.
				"status": {Value: "pending", Mutability: "mutable", AuthorityScope: "principal"},
				// Extras (not in governance_proposal spec but ValidateADD allows them).
				// Kept free-form; if a v3.10 bump adds these to the spec they'll
				// flow in transparently.
				"created_at":        {Value: now, Mutability: "immutable"},
				"source_t_hook_urn": {Value: string(n.URN), Mutability: "immutable"},
				"fires_at_t":        {Value: currentT, Mutability: "immutable"},
				"proposed_envelope": {Value: reactTemplate, Mutability: "immutable"},
				"owner_urn":         {Value: ownerURN, Mutability: "immutable"},
			},
		})
	}

	return envelopes
}

// slugFromURN returns the final colon-delimited segment of a URN. Used to
// compose a readable proposal URN suffix; the full URN is fine for the
// source_t_hook_urn property, but the slug keeps the proposal URN short.
func slugFromURN(urn graph.URN) string {
	s := string(urn)
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s
	}
	return s[i+1:]
}

// ----------------------------------------------------------------------------
// Runtime integration: the goroutine loop and manual-trigger helper
// ----------------------------------------------------------------------------

// SetSweepActor overrides the actor URN used by the sweep goroutine.
// Must be called before RunTimedSweep is started; not concurrency-safe.
// If never called, DefaultSweepActor is used.
func (rt *Runtime) SetSweepActor(actor graph.URN) {
	rt.sweepActor = actor
}

// RunTimedSweep runs the sweep at the given interval until ctx is canceled.
// An interval <= 0 disables the sweep (function returns immediately after
// logging); pass 0 via --sweep-interval to opt out without recompiling.
//
// The goroutine ticks on a time.Ticker and calls (*Runtime).SweepTick on
// each tick. Shutdown is clean on ctx.Done — the deferred ticker.Stop
// releases the runtime timer.
func (rt *Runtime) RunTimedSweep(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		log.Printf("sweep: disabled (interval=%v)", interval)
		return
	}
	log.Printf("sweep: starting (interval=%v)", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("sweep: shutdown")
			return
		case <-ticker.C:
			rt.SweepTick()
		}
	}
}

// SweepTick performs one sweep pass against the current runtime state.
// Exported so tests and operators can trigger a manual sweep without the
// goroutine — useful for time-travel debugging ("what would fire at T=X?").
//
// Errors from the underlying ApplyProgram are logged and not propagated:
// the next tick will retry. A batch failure leaves state unchanged
// (ApplyProgram is all-or-nothing).
func (rt *Runtime) SweepTick() {
	actor := rt.sweepActor
	if actor == "" {
		actor = DefaultSweepActor
	}
	state := rt.State()
	envelopes := SweepOnce(state, CurrentSweepTDay(), actor, rt.logSeq.Load())
	if len(envelopes) == 0 {
		return
	}
	if _, err := rt.ApplyProgram(envelopes); err != nil {
		log.Printf("sweep: ApplyProgram failed for %d envelopes: %v", len(envelopes), err)
	}
}
