# moos-kernel

Canonical reference: `ffs0/kb/research/20260402-codex-unified.md`
Ontology: `ffs0/kb/superset/ontology.json` (v3.1)
Gate closed: T=153

---

## The Rule

Nothing happens except rewrites. Four operations:

```
ADD    — create a new node with typed properties
LINK   — create a new relation (hyperedge) connecting two nodes
MUTATE — change one typed property value on one existing node
UNLINK — remove one relation
```

## Nomenclature (enforced)

| Use | Not this |
|-----|----------|
| node | object, element, vertex |
| relation | binding, edge, wire, association |
| rewrite | morphism, update, mutation |
| rewrite_category (WF01-WF15) | named relation, UML association |
| property | field, payload, attribute |
| operad | schema, grammar |
| interaction_node | transition, event, message |
| `_urn` suffix | `_ref` |

## Package Structure

```
internal/graph      — pure types (no IO): Node, Relation, Property, Rewrite, GraphState
internal/fold       — pure catamorphism: Evaluate, Replay, EvaluateProgram
internal/operad     — type system: Registry (WF01-WF15), ValidateLINK, ValidateMUTATE
internal/kernel     — effect layer: Runtime, Store, LogStore, MemStore
internal/transport  — HTTP adapter (13 routes)
internal/mcp        — MCP JSON-RPC 2.0 (SSE + stdio)
cmd/moos            — entry point
```

## Safety

- Never commit `ffs0/secrets/` files
- Never embed API keys in code
- `ffs0/` is a sibling repo — not part of this module

## Running

```bash
go run ./cmd/moos \
  --ontology ../../ffs0/kb/superset/ontology.json \
  --log /tmp/moos.log \
  --seed
```
