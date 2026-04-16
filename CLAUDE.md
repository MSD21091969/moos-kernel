# moos-kernel

Ontology: `ffs0/kb/superset/ontology.json` (v3.6 — 40 node types, WF01–WF19)
Canonical reference: `ffs0/kb/research/20260408-foundation-t158.md`
T=164 delta: `ffs0/kb/research/20260414-t164-session-channel-purpose.md`
Gate: T=166

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
| rewrite_category (WF01–WF19) | named relation, UML association |
| property | field, payload, attribute |
| operad | schema, grammar |
| interaction_node | transition, event, message |
| `_urn` / `_urns` suffix | `_ref` / `_refs` |

Relations are truth. Properties never duplicate topology.

---

## Package Structure

```
internal/graph      — pure types (no IO): Node, Relation, Property, Rewrite, Envelope,
                      GraphState, URN, Stratum, RewriteCategory
internal/fold       — pure catamorphism: Evaluate, Replay, EvaluateProgram
internal/operad     — type system: Registry (WF01–WF19), ValidateLINK,
                      ValidateMUTATE, loader (loads external ontology.json)
internal/kernel     — effect layer: Runtime, Store, LogStore, MemStore
internal/hdc        — Hyperdimensional Computation: spectral.go, fiber.go,
                      crosswalk.go, encode.go, live_index.go
internal/reactive   — Watch/React/Guard engine: evaluates watcher→reactor
                      chains against incoming rewrites (read-only)
internal/transport  — HTTP adapter (REST routes)
internal/mcp        — MCP JSON-RPC 2.0 (SSE + stdio)
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
| `--ontology` | (none) | Path to ontology.json; omit for no type validation |
| `--log` | (none) | JSONL log path; omit for in-memory (non-persistent) |
| `--listen` | `:8000` | HTTP transport address |
| `--mcp-addr` | `:8080` | MCP SSE server address |
| `--mcp-stdio` | false | Also run MCP on stdin/stdout |
| `--stdio-only` | false | MCP stdio only — no HTTP/SSE |
| `--seed` | false | Seed infrastructure nodes (idempotent) |
| `--seed-user` | `sam` | Username for seed node |
| `--seed-ws` | `hp-laptop` | Workstation name for seed node |

---

## Testing

```bash
go test ./internal/...
```

Test files: `internal/fold/fold_test.go`, `internal/hdc/hdc_test.go`, `internal/hdc/spectral_test.go`,
`internal/hdc/fiber_test.go`, `internal/hdc/crosswalk_test.go`, `internal/kernel/runtime_reactive_test.go`,
`internal/reactive/engine_test.go`. No external test dependencies — stdlib only.

---

## Development Conventions

- **No external dependencies** — stdlib only (`go.mod` has no require block).
- **Layer discipline**: `graph` has no IO; `fold` is pure; only `kernel` has effects.
- **Immutability**: `Registry` is read-only after load. `GraphState` is replaced on each rewrite, never mutated in place.
- **Operad validation before fold**: `operad` validates type/port/authority; `fold` enforces structural invariants.
- **Reactive is read-only**: `reactive.Engine` never mutates graph state directly; returns proposed `[]Envelope` for the caller to apply.
- **HDC is derived**: `hdc.LiveIndex` is recomputed from state after each apply.

---

## Safety

- Never commit `ffs0/secrets/` files.
- Never embed API keys in code.
- `ffs0/` is a sibling repo — not part of this module.
