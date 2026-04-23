# moos-kernel

Ontology: `ffs0/kb/superset/ontology.json` (**v3.12.0** — 52 node types, 20 WFs / WF01–WF20)
Canonical spec: `ffs0/kb/research/kernel/20260417-t187-kernel-proper.md` (M1-M20, live)
Active implementation-spec docs (kb/research/session/): `20260419-t169-session-generalization.md` (5-facet tuple), `20260422-t172-cowork-as-occupant.md` (platform-host), `20260422-t172-wolframs-court-social-topology.md` (persona court)

Archived references (retrieve from `ffs0/dev/reference/research-archive/` when needed): `20260408-foundation-t158.md` (original foundation paper), `20260414-t164-session-channel-purpose.md`, `20260418-t168-*.md` (S1/S0 substrate lingo, ontology audit), `20260420-t170-*.md` (functorial semantics spine, S0 materialization), per-round conversation summaries.

---

## The Rule

Nothing happens except rewrites. Four operations only:

```
ADD    — create a new node with typed properties
LINK   — create a new relation (hyperedge) connecting two nodes
MUTATE — change one typed property value on one existing node
UNLINK — remove one relation
```

state(t) = fold(log[0..t]). Log is truth. State is derived.

---

## Nomenclature (enforced)

| Use | Not this |
|-----|----------|
| node | object, element, vertex |
| relation | binding, edge, wire, association |
| rewrite | morphism, update, mutation |
| rewrite_category (WF01–WF20) | named relation, UML association |
| property | field, payload, attribute |
| operad | schema, grammar |
| interaction_node | transition, event, message |
| `_urn` / `_urns` suffix | `_ref` / `_refs` |

Relations are truth. Properties never duplicate topology.

---

## Runtime gates (T=171 round 11)

Every envelope passes two gates in `Runtime.Apply` / `Runtime.ApplyProgram` before fold:

- **§M11 liveness** (`internal/kernel/liveness.go` — `checkLivenessM11`): envelope's emitter must have a session context. Explicit `env.session_urn` wins; fallback is reverse-`has-occupant` lookup from `env.actor` (unambiguous = pass; ambiguous or absent = reject). Runs against batch-initial state (emitter pre-existence rule; see ffs0 impl-plan §2.4 — now in archive).
- **§M12 admin-capability** (`checkLivenessM12`): envelope classified admin-scope by `operad.(*Registry).AdminScopeRewrite`. Admin scope = ADD/MUTATE on ontology-governed types (`system_instruction`, `gate`, `twin_link`, `transport_binding`, `kernel`) OR MUTATE of a property with `authority_scope: "kernel"` on a non-kernel node. Actor must hold WF02 superadmin via `operad.CheckAdminCapability`. Runs against working-state in ApplyProgram loop (catches intra-batch ADD-then-MUTATE bypass, PR #31 fix).

**System-internal allowlist** (`operad.SystemInternalEnvelope`) precedes both gates: kernel-URN actors + ADD of infrastructure types (`user`, `workstation`, `kernel`) bypass by design. `SeedIfAbsent` additionally bypasses §M11 structurally for bootstrap.

**Replay is prospective-only**: `fold.Replay` does not call `checkLiveness`. Pre-gate persisted logs rebuild identically.

---

## Package Structure

```
internal/graph      — pure types (no IO): Node, Relation, Property, Rewrite, Envelope
                      (+ optional SessionURN field for explicit §M11 context),
                      GraphState (+ NodesByType / RelationsBySrc / RelationsByTgt indexes),
                      URN, Stratum, RewriteCategory
internal/fold       — pure catamorphism: Evaluate, Replay, EvaluateProgram
                      (maintains GraphState indexes on ADD/LINK/UNLINK)
internal/operad     — type system: Registry (WF01–WF20) with Version field for /healthz,
                      AdditionalPortPairs per WF (consumed from ontology.json),
                      strict ValidateLINK (pair must be declared; pair-level src/tgt
                      type enforcement), ValidateMUTATE, session_context.go
                      (ResolveSessionForEnvelope, SystemInternalEnvelope,
                      AdminScopeRewrite method, authorityScopeForField),
                      occupancy.go (ResolveSessionOccupant, CheckAdminCapability,
                      RotateSessionOccupant atomic-program emitter)
internal/kernel     — effect layer: Runtime, Store, LogStore, MemStore;
                      liveness.go (§M11 + §M12 gates); applyWithOptions (skipLiveness
                      for SeedIfAbsent bootstrap); sweep loop (RunTimedSweep /
                      SweepTick) emitting WF13 governance_proposals per
                      t_hook.firing_state transitions
internal/reactive   — predicate evaluator (EvaluateThookPredicate, 10+ §M14 kinds
                      incl. when_capability), Watch/React/Guard engine
internal/transport  — HTTP adapter: state/log/rewrites/programs/operad/hdc,
                      /t-hook/evaluate (GET + POST batch), /t-cone,
                      /twin/ingest, /fold (+ SSE stream), /healthz
                      (reports ontology_version field, T=171 PR #26)
internal/hdc        — Hyperdimensional Computation: spectral, fiber,
                      crosswalk, encode, live_index
internal/mcp        — MCP JSON-RPC 2.0 (SSE + stdio + Streamable HTTP)
internal/tday       — shared T-day epoch (T0 + Now + At)
cmd/moos            — entry point
```

---

## Running

```bash
go run ./cmd/moos \
  --ontology ../../ffs0/kb/superset/ontology.json \
  --log /tmp/moos.log \
  --seed
```

### CLI Flags

| Flag | Default | Purpose |
|------|---------|--------|
| `--ontology` | (none) | Path to ontology.json; omit for no type validation (liveness/admin gates also bypass in registry-less mode) |
| `--log` | (none) | JSONL log path; omit for in-memory (non-persistent) |
| `--listen` | `:8000` | HTTP transport address |
| `--mcp-addr` | `:8080` | MCP SSE server address |
| `--mcp-stdio` | false | Also run MCP on stdin/stdout |
| `--stdio-only` | false | MCP stdio only — no HTTP/SSE |
| `--seed` | false | Seed infrastructure nodes (idempotent, liveness-bypassed) |
| `--seed-user` | `sam` | Username for seed node |
| `--seed-ws` | `hp-laptop` | Workstation name for seed node |
| `--sweep-interval` | `30s` | T-hook sweep tick (0 disables) |
| `--quic-addr` | (none) | UDP address for HTTP/3 QUIC listener; requires `--tls-cert` and `--tls-key` |
| `--tls-cert` | (none) | TLS certificate (PEM) for QUIC listener |
| `--tls-key` | (none) | TLS private key (PEM) for QUIC listener |

---

## Testing

```bash
go test ./...
```

| Package | Files |
|---------|-------|
| `internal/fold` | `fold_test.go` |
| `internal/graph` | `state_test.go` |
| `internal/hdc` | `hdc_test.go`, `spectral_test.go`, `fiber_test.go`, `crosswalk_test.go` |
| `internal/kernel` | `runtime_reactive_test.go`, `sweep_test.go`, `liveness_test.go` |
| `internal/operad` | `occupancy_test.go`, `occupancy_rotate_test.go`, `loader_test.go`, `loader_port_pairs_test.go`, `validate_link_pair_test.go`, `session_context_test.go` |
| `internal/reactive` | `engine_test.go`, `predicate_test.go`, `predicate_extended_test.go` |
| `internal/tday` | `tday_test.go` |
| `internal/transport` | `thook_test.go`, `tcone_test.go` |

Integration test gated behind `MOOS_INTEGRATION=1` (reads sibling `ffs0/kb/superset/ontology.json`). Race-detector run requires cgo/gcc.

---

## Development Conventions

- **No external dependencies** beyond quic-go (required for `--quic-addr`). Stdlib-first; stdlib-only except QUIC.
- **Layer discipline**: `graph` has no IO; `fold` is pure; only `kernel` has effects.
- **Immutability**: `Registry` is read-only after load. `GraphState` is replaced on each rewrite, never mutated in place.
- **Operad validation before fold**: `operad` validates type/port/authority; `fold` enforces structural invariants and maintains indexes.
- **Reactive is read-only**: `reactive.Engine` never mutates graph state directly; returns proposed `[]Envelope`. Sweep runs on its own goroutine and applies through the normal `ApplyProgram` path.
- **HDC is derived**: `hdc.LiveIndex` is recomputed from state after each apply.
- **Single T-day epoch**: `internal/tday` is the one source of `T0` + `Now()`.
- **Firing semantics**: sweep proposes via WF13 governance; never auto-applies. `t_hook.firing_state` state machine `pending → proposed → approved|rejected → applied / closed`.
- **Liveness/admin gating**: §M11 preflight against initial state (emitter must pre-exist); §M12 per-envelope inside ApplyProgram working-state loop (targets evolve mid-batch). Prospective-only on replay.

---

## Post-§M11 actor discipline for envelope authors

| Actor | When | Gate behavior |
|---|---|---|
| `urn:moos:agent:<short>` | default for agent-driven work | §M11 inferred path (one session occupied); §M12 subject to superadmin if admin-scope |
| `urn:moos:kernel:<ws>.<name>` | ontology-governed ADDs; kernel-authority MUTATEs on non-kernel nodes; WF19 opens-on | §M11 + §M12 allowlisted |
| `urn:moos:user:sam` | NOT for user-space envelopes | §M11 fails (owner, not occupant); only valid inside SeedIfAbsent bootstrap |

Set `env.session_urn` explicitly only when the agent occupies multiple sessions.

---

## Safety

- Never commit `ffs0/secrets/` files.
- Never embed API keys in code.
- `ffs0/` is a sibling repo — not part of this module.
- Build artifacts (`moos-kernel*.exe`, `moos-kernel.exe~`, `moos-kernel-pre*.exe`) and ephemeral state snapshots are gitignored; do not commit them.
- Repo at `github.com/Collider-Data-Systems/moos-kernel` since T=172.
