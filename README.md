# mo:os kernel

A categorical hypergraph rewriting kernel for distributed knowledge work. Go, stdlib-first.

## What it is

mo:os is a **semantic functorial network over distributed compute**, where all components — hardware and programs — are considered categorically. The hypergraph (HG) is the source of truth; state is derived from a log of four primitive rewrites:

```
ADD    — create a node with typed properties
LINK   — create a relation (hyperedge) between two nodes via a typed port pair
MUTATE — change one property value on one existing node
UNLINK — remove a relation
```

`state(t) = fold(log[0..t])`. The log is append-only; the kernel is a fold over it. Replay is prospective-only.

The session is the centerpiece. A session is a 5-facet scoped context — `(scope, purpose) × (host, owner, occupant)` — where human and AI inference happens; its relations both make it findable (the G-direction adjunction observing surfaces like Drive, Gmail, Calendar, GitHub) and capable of writing the programs that pull the levers (the F-direction emitting envelopes through leaves). Leaves are whichever fluid structure surfaces values back to the session's open arguments.

## What's in the box

| Layer | Path | Role |
|---|---|---|
| `internal/graph` | pure types | `Node`, `Relation`, `Property`, `Rewrite`, `Envelope`, `GraphState` (with indexes), `URN`, `Stratum`, `RewriteCategory`. No IO. |
| `internal/fold` | pure catamorphism | `Evaluate`, `Replay`, `EvaluateProgram`. Maintains state indexes on ADD/LINK/UNLINK. |
| `internal/operad` | type system | `Registry` (ontology v3.13.0: 52 node types; WFs WF01–WF20 promoted, WF21 proposed in v3.14 candidate set; current typed `RewriteCategory` constants in `internal/graph` run through WF19), strict port-pair `ValidateLINK`, `ValidateMUTATE`, occupancy resolution, admin-capability walks. |
| `internal/kernel` | effect layer | `Runtime`, `Store`, `LogStore`/`MemStore`. §M11 session-liveness + §M12 admin-capability gates. Sweep loop emits WF13 governance proposals on `t_hook.firing_state` transitions. |
| `internal/reactive` | predicate evaluator | Watch / React / Guard engine; `EvaluateThookPredicate` covers 10+ §M14 predicate kinds. |
| `internal/transport` | HTTP + SSE | `/state/*` (e.g. `/state/nodes`, `/state/relations`), `/log`, `/rewrites`, `/programs`, `/operad/*` (e.g. `/operad/node-types`), `/hdc/*`, `/t-hook/evaluate`, `/t-cone`, `/twin/ingest`, `/fold` (with SSE), `/healthz`. |
| `internal/hdc` | hyperdimensional computing | spectral, fiber, crosswalk, encode, live-index. |
| `internal/mcp` | MCP JSON-RPC | SSE + stdio + Streamable HTTP. |

Layer discipline: `graph` has no IO; `fold` is pure; only `kernel` has effects.

## Running

```bash
go run ./cmd/moos \
  --ontology ../ffs0/kb/superset/ontology.json \
  --log /tmp/moos.log \
  --listen :8000 \
  --mcp-addr :8080 \
  --seed
```

Key flags:

| Flag | Default | Purpose |
|---|---|---|
| `--ontology` | (none) | path to `ontology.json`; without it, ontology-backed type validation is disabled (the runtime falls back to an empty registry); §M11 + §M12 gates still run |
| `--log` | (none) | JSONL log path; in-memory if omitted |
| `--listen` | `:8000` | HTTP transport |
| `--mcp-addr` | `:8080` | MCP SSE |
| `--sweep-interval` | `30s` | t-hook sweep cadence (0 disables) |
| `--quic-addr` | (none) | UDP/HTTP3 listener (requires `--tls-cert` + `--tls-key`) |

Direct dependencies are stdlib-only except `quic-go` (gated behind `--quic-addr`); transitive dependencies include `golang.org/x/net` and `golang.org/x/crypto` via `quic-go`.

## Testing

```bash
go test ./...
```

Race-detector requires cgo. Integration tests gated behind `MOOS_INTEGRATION=1` (read sibling `ffs0/kb/superset/ontology.json`).

## Runtime gates (live since T=171 round 11)

Every envelope passes two gates before fold:

- **§M11 liveness** — emitter must have a session context. Explicit `env.session_urn` wins; fallback is reverse-`has-occupant` lookup from `env.actor` (single-session = pass; ambiguous or absent = reject).
- **§M12 admin-capability** — admin-scope rewrites (ADDs on ontology-governed types, MUTATEs of `authority_scope: kernel` properties on non-kernel nodes) walk `WF02 governs` from actor through `role:superadmin` to verify capability.

System-internal allowlist (kernel-URN actors emitting infrastructure types) bypasses both. `SeedIfAbsent` additionally bypasses §M11 structurally for bootstrap.

Fold-time replay does **not** re-check liveness — pre-gate persisted logs rebuild identically.

## Ontology

[`ffs0/kb/superset/ontology.json`](https://github.com/Collider-Data-Systems/ffs0) (private workspace) — v3.13.0 — 52 node types, WFs WF01–WF21. Loaded at boot; immutable thereafter.

Selected node types: `session`, `program`, `purpose`, `agent`, `kernel`, `channel`, `knowledge_item`, `claim`, `t_hook`, `reactor`, `watcher`, `tool_call`, `tool_result`, `external_op`, `compute`, `storage`, `harness`, `workflow`, `group`, `role`, `gate`, `system_instruction`, `transport_binding`, `twin_link`, `governance_proposal`, `grammar_fragment`.

WF categories include WF01 owns/owned-by, WF02 governs/delegates-to, WF12 provides-kb/kb-source, WF13 governance-proposes/proposed-by, WF18 scheduled-after/scheduled-before, WF19 opens-on/has-occupant + occupied-by/is-occupant-of, WF20 ceremony promotion.

## Federation

Twin kernels ride the same hypervisor host via `twin_link` edges and the WF16 router (`Collider-Data-Systems/moos-router`). Cross-machine federation rides Cloudflare tunnels (Z440 ↔ hp-laptop). The router shards by URN prefix; each kernel keeps its own sovereign log.

## Status

Round-11 closed at T=174 (April 24, 2026, just past midnight CEST). Round-12 active (T=175): five v3.14 grammar_fragment proposals expanding the ontology with `derivation` (reify session inference), `clock` (generalized time fabric — `t_hook` becomes a special case), WF21 `causes/caused-by` (causation distinct from succession), `substrate` property (where node-truth lives), and leaf-firing-state semantics.

MVP target: T=190 (May 10, 2026) — six gates (G1–G6) toward `purpose:sam.mvp-sovereign-knowledge-os`.

## Repository layout

```
cmd/moos          # entry point
internal/...      # see "What's in the box" table
go.mod            # stdlib + quic-go only
```

## Companion repositories

- **moos-router** — WF16 federation gateway; URN-prefix shard routing
- **ffs0** — private workspace + doctrine notes + skill library + ontology source-of-truth

## License

(TBD — assignment in progress)

## Contact

Maintained by [@Collider-Data-Systems](https://github.com/Collider-Data-Systems). Two teams: **Sam** (kernels + delegates + most sessions), **Moos** (multimodal diary curation).
