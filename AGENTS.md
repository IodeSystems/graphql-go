# AGENTS.md — graphql-go

IodeSystems fork of `graphql-go/graphql`. Single-module Go library implementing the GraphQL spec, with fork-specific performance optimizations.

## Commands

```
go test ./...              # all tests (root + language/* + testutil)
go test -run TestX ./...   # single test across all packages
go test -race ./...        # race detector (CI runs this for coverage)
go vet ./...               # static analysis (CI runs after tests)
go test -bench=. -benchmem # benchmarks (requires -bench flag)
bin/perf                   # compare benchmarks between two git refs, updates README
```

No linter, formatter, build step, or codegen. `go fmt` and `go vet` are the only style gates (per CONTRIBUTING.md).

## Package Layout

- **Root (`github.com/IodeSystems/graphql-go`)** — the library. Entry points: `graphql.Do(params)` (convenience), `graphql.PlanQuery()` + `graphql.ExecutePlan()` (cached), `graphql.ExecutePlanAppend()` (zero-allocation JSON). Schema types, executor, planner, validator, scalars, values, extensions.
- **`language/`** — parser, lexer, AST, printer, visitor, source, kinds, location, typeInfo. Mirrors `graphql-js` language layer.
- **`gqlerrors/`** — error types and formatting.
- **`testutil/`** — test helpers only (StarWarsSchema, rule harness). Not part of the public API.
- **`benchutil/`** — benchmark schemas (wide, list). No tests, only runs under `-bench=.`.
- **`examples/`** — standalone example programs. No tests, not imported by the library.

## Fork-Specific APIs (not in upstream)

- **`PlanQuery(schema, doc, opName) (*Plan, error)`** — pre-compute execution plan. Cache the `*Plan`, reuse across requests. Bound to the `*Schema` pointer; rebuild schema → stale plans.
- **`ExecutePlan(plan, params) *Result`** — execute a cached plan. Returns `map[string]interface{}` result tree (caller JSON-marshals).
- **`ExecutePlanAppend(plan, params, dst []byte) ([]byte, []FormattedError)`** — zero-allocation JSON emission. Writes `{"data":...,"errors":[...]}` directly to `dst`. No intermediate map tree.
- **`Field.ResolveAppend`** — opt-in resolver that writes JSON bytes directly. Skips Serialize, leafEmitter, and result boxing. Extensions hooks do NOT fire on ResolveAppend fields.
- **`ScalarConfig.AppendJSON`** — optional hook for custom scalars to emit JSON bytes directly.

### ExecuteParams flags (append-mode)

| Flag | Default | Meaning |
|---|---|---|
| `PreserveInfoPath` | `false` | When false (default), `ResolveInfo.Path` is nil under `ExecutePlanAppend`. Set true if resolvers read `info.Path`. No effect on `ExecutePlan`. |
| `ConcurrentThunks` | `false` | When true, delegates to `ExecutePlan` + `json.Marshal` for breadth-first thunk dethunking. Set if resolvers return `func() (interface{}, error)` thunks that kick off goroutines. |
| `RetainArgs` | `false` | When false (default), the executor pools `ResolveParams.Args` via `sync.Pool`. Set true if resolvers retain `p.Args` past the call. |

## Testing

- Most root-package tests are `package graphql_test` (external). A few are `package graphql` (internal, `_internal_test.go` suffix).
- `testutil.StarWarsSchema` is the canonical test schema used across most root tests.
- Coverage CI excludes `examples/*`, `benchutil`, `testutil`, and `language/ast` (trivial interface-compliance stubs). Replicate with:
  ```
  PKGS=$(go list ./... | grep -vE '/examples/|/benchutil$|/testutil$|/language/ast$')
  go test -cover -race -coverpkg="$(echo $PKGS | tr '\n' ',')" $PKGS
  ```

## Design Docs

- `docs/plan.md` — detailed design doc for the append-mode execution pipeline (Phases 1–7). Read before modifying `plan.go`, `executor.go`, or the scalar emitters in `scalars.go`.

## CI

CircleCI runs `go test ./...` then `go vet ./...` on Go 1.21 and Go latest. Coverage job uploads to Coveralls.
