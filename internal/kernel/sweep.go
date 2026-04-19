package kernel

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"moos/kernel/internal/graph"
	"moos/kernel/internal/reactive"
	"moos/kernel/internal/tday"
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

// CurrentSweepTDay returns the current calendar T-day. Thin wrapper over
// tday.Now so the sweep's wall-clock source is the same one the transport
// layer uses — the round-9 review flagged the previous local epoch as a
// drift risk, and T=169 consolidated both to internal/tday.
//
// Kept exported for back-compat with callers that imported it before the
// tday package existed; new code should call tday.Now directly.
func CurrentSweepTDay() int { return tday.Now() }

// DefaultSweepActor is the actor URN used by the sweep when ADDing
// governance_proposal nodes. Override via Runtime.SetSweepActor before
// starting the sweep goroutine if a more specific kernel URN is known
// (e.g. urn:moos:kernel:hp-laptop.primary).
const DefaultSweepActor graph.URN = "urn:moos:kernel:sweep"

// SweepOnce inspects the given state and returns the list of
// governance_proposal ADD envelopes for every pending t_hook whose
// predicate evaluates true at currentT.
//
// Deterministic (given state + currentT + actor + baseLogSeq + now): does
// not mutate state or perform IO. The caller (Runtime.SweepTick) passes
// a snapshot-time `now` so all proposals in one sweep share a single
// timestamp; this also makes the function testable without a clock stub.
//
// Idempotency: hooks that already have a matching governance_proposal
// (by source_t_hook_urn equality) are skipped.
//
// baseLogSeq is used only for URN disambiguation across hooks within a
// single tick — it's appended to the URN suffix so two hooks firing in
// the same tick get distinct proposal URNs. The offset starts at 1 so
// the sequence matches the log_seq that ApplyProgram will assign when
// the envelopes actually append (PR #12 review — Gemini).
func SweepOnce(state graph.GraphState, currentT int, actor graph.URN, baseLogSeq int64, now time.Time) []graph.Envelope {
	// Build the idempotency set via the by-type accessor: only
	// governance_proposal nodes need to be visited. O(proposals) on
	// production states (indexed); O(all-nodes) fallback on un-indexed
	// test fixtures.
	proposedHooks := make(map[graph.URN]struct{})
	for _, propURN := range state.NodesOfType("governance_proposal") {
		n, ok := state.Nodes[propURN]
		if !ok {
			continue // index drift (should not happen); skip
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
	nowStr := now.UTC().Format(time.RFC3339)

	// Walk only the t_hook bucket rather than every node in the graph.
	for _, hookURN := range state.NodesOfType("t_hook") {
		n, ok := state.Nodes[hookURN]
		if !ok {
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
		// Evaluate against the state we received. EvaluateThookPredicate
		// handles the json round-trip when the value arrives from log
		// replay as map[string]any; returns false on malformed kinds.
		// (Previously took &stateCopy to defend against mutation inside
		// the evaluator — the evaluator is pure, so passing &state directly
		// is both correct and avoids a misleading shallow copy whose
		// underlying maps still aliased the original.)
		if !reactive.EvaluateThookPredicate(predProp.Value, &state, currentT) {
			continue
		}

		// Compose the proposal envelope. The URN encodes the source hook
		// slug + calendar-T + sequence offset so operators can trace it.
		// Offset starts at 1 to align with the log_seq ApplyProgram will
		// later assign to the envelope (baseLogSeq+1, baseLogSeq+2, ...).
		hookSlug := slugFromURN(n.URN)
		emitted++
		proposalURN := graph.URN(fmt.Sprintf("urn:moos:proposal:kernel.%s-t%d-seq%d", hookSlug, currentT, baseLogSeq+emitted))

		title := fmt.Sprintf("Fire t_hook %s at T=%d", n.URN, currentT)

		// Extras — only set when the source hook actually carries them, so
		// we don't ADD a governance_proposal with nil-valued breadcrumbs
		// that give approvers a false sense of traceability (PR #12
		// review — Copilot).
		props := map[string]graph.Property{
			// Required immutable property per v3.9 ontology.
			"title": {Value: title, Mutability: "immutable"},
			// Mutable-with-principal-scope per spec; approver flips this.
			"status":            {Value: "pending", Mutability: "mutable", AuthorityScope: "principal"},
			"created_at":        {Value: nowStr, Mutability: "immutable"},
			"source_t_hook_urn": {Value: string(n.URN), Mutability: "immutable"},
			"fires_at_t":        {Value: currentT, Mutability: "immutable"},
		}
		if p, ok := n.Properties["react_template"]; ok && p.Value != nil {
			props["proposed_envelope"] = graph.Property{Value: p.Value, Mutability: "immutable"}
		}
		if p, ok := n.Properties["owner_urn"]; ok && p.Value != nil {
			props["owner_urn"] = graph.Property{Value: p.Value, Mutability: "immutable"}
		}

		envelopes = append(envelopes, graph.Envelope{
			RewriteType: graph.ADD,
			Actor:       actor,
			NodeURN:     proposalURN,
			TypeID:      "governance_proposal",
			Properties:  props,
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

// sweepActorMu guards the sweepActor field on Runtime. Gemini flagged the
// bare read in SweepTick as a data race against SetSweepActor; in practice
// the race is benign (URN is a string and the runtime is single-writer at
// startup), but Go's race detector would catch it and it costs nothing to
// fix (PR #12 review — Gemini).
var sweepActorMu sync.RWMutex

// SetSweepActor overrides the actor URN used by the sweep goroutine.
// Thread-safe: may be called at any point in the runtime lifecycle.
// If never called, DefaultSweepActor is used at tick time.
func (rt *Runtime) SetSweepActor(actor graph.URN) {
	sweepActorMu.Lock()
	defer sweepActorMu.Unlock()
	rt.sweepActor = actor
}

// sweepActorLocked reads the current sweep actor under the package mutex.
// Returns the default when the runtime's field is empty.
func (rt *Runtime) sweepActorLocked() graph.URN {
	sweepActorMu.RLock()
	defer sweepActorMu.RUnlock()
	if rt.sweepActor == "" {
		return DefaultSweepActor
	}
	return rt.sweepActor
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
	actor := rt.sweepActorLocked()
	state := rt.State()
	envelopes := SweepOnce(state, CurrentSweepTDay(), actor, rt.logSeq.Load(), time.Now())
	if len(envelopes) == 0 {
		return
	}
	if _, err := rt.ApplyProgram(envelopes); err != nil {
		log.Printf("sweep: ApplyProgram failed for %d envelopes: %v", len(envelopes), err)
	}
}
