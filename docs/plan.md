# Append-mode execution

Working doc for the next perf push on top of the plan cache. Read at
session start; update as decisions land.

## How to use this file

Source of truth for this workstream's priority order and decisions.
Item shape: **The push** (why & where it sits) → **Done** (one-line
entries with commit hashes) → **Todo** (commit-sized chunks with
rough effort) → **Followups** (mid-flight discoveries that don't
block). Drop the entry once every Todo is checked — git history is
the record.

---

## Why this exists

The plan cache eliminated per-request parse + validate + plan. What's
left in `ExecutePlan` (`plan.go:545`) is the inherently-per-request
work: walking the plan, resolving fields, building a result tree.
That tree is `map[string]interface{}` with `interface{}` boxing on
every scalar, allocated fresh per request at every object level. The
caller then JSON-marshals it once at the egress boundary, walking the
same tree a second time.

For a typical gateway request through `BenchmarkProtoSchemaExec` (gw
repo): ~430 µs / 972 allocs end-to-end. The dispatcher itself is
~185 µs / 174 allocs; the remaining **~245 µs / ~800 allocs is
executor + final marshal**. The plan already carries everything the
executor needs to skip the map tree entirely:

- `selectionPlan.fields` source-ordered, pre-collected
- `fieldPlan.responseKey` / `fieldName` / `fieldDef` / `returnType`
  pre-resolved
- `fieldPlan.args.static` pre-built for all-literal-args fields
- `fieldPlan.skipPredicate` nil in the constant-true common case
- `fieldPlan.sub` pre-planned sub-selection tree
- `fieldPlan.abstractAlternatives` per-concrete-type sub-plans

The append-mode strategy uses that information to write JSON straight
to a byte buffer — no `map[string]interface{}`, no scalar boxing in
the result tree, no second marshal pass. The buffer **is** the
response body.

---

## Strategy

Add `ExecutePlanAppend` as a sibling to `ExecutePlan`. Same plan,
same `ExecuteParams`, but the output is appended to a `[]byte` (and
a side slice of errors), not a `*Result`. Existing callers keep
`ExecutePlan`; new ones opt in.

```go
// Appends `{"data":<data>,"errors":[...]}` (errors omitted when
// empty) to dst per the GraphQL HTTP spec response shape, and
// returns dst plus any spec-level errors that occurred before
// data assembly began (e.g. variable-coercion failures). Field-
// level errors are written into the response bytes; the returned
// slice is for cases where data is structurally unavailable.
func ExecutePlanAppend(plan *Plan, p ExecuteParams, dst []byte) (
    []byte,
    []gqlerrors.FormattedError,
)
```

`io.Writer`-shaped wrapper lives next to it (`WritePlanResult(plan,
p, w) error`), trivially built on the append form by writing the
returned slice in one `w.Write(buf)` at the end — caller decides
between streaming and buffered.

### Plan-time additions

Two new fields on `fieldPlan` (`plan.go:51`), both populated in
`PlanQuery`:

```go
type fieldPlan struct {
    // ... existing fields ...

    // responseKeyJSON is the pre-encoded JSON object key for this
    // field, including the leading quote, escaped key bytes, closing
    // quote, and trailing colon. Ready to append between fields.
    // Example: []byte(`"hello":`) for responseKey "hello".
    responseKeyJSON []byte

    // leafEmitter is set when returnType (after unwrapping NonNull
    // + List) is a Scalar or Enum and a typed emitter is registered
    // for it. nil for object/abstract returns and for scalars that
    // fall back to Serialize + json.Marshal. Called with the result
    // of fieldDef.Type.Serialize(value); writes the JSON form of
    // that result.
    leafEmitter func(dst []byte, v interface{}) []byte
}
```

`Scalar` gains an optional `AppendJSON func([]byte, interface{}) []byte`
hook. When set, the planner uses it for `leafEmitter`. Built-in
scalars (`String`, `Int`, `Float`, `Boolean`, `ID`) ship with default
emitters in this package; custom scalars opt in. The fallback path
(`leafEmitter` nil) is `Serialize` → `json.Marshal` → append — slow
but correct.

### Per-request hot path

`ExecutePlanAppend`:

1. Acquire a `*[]byte` scratch from `sync.Pool` (separate from `dst`
   so callers can target their own buffer).
2. Coerce variables (same `getVariableValues` call).
3. Walk `plan.root` via `writePlannedSelection(dst, eCtx, sp,
   source, parentType, path)` — the append-mode mirror of
   `executePlannedSelection`.
4. On return: emit `{"data":` + walked-bytes + (`,"errors":[...]`
   if any) + `}` into the caller's `dst`. Return `dst, nil`.

`writePlannedSelection` does:

```
emit `{`
for i, fp := range sp.fields:
    if fp.skipPredicate != nil && !fp.skipPredicate(vars): continue
    if i > 0: emit `,`
    emit fp.responseKeyJSON  // pre-encoded
    writePlannedField(dst, eCtx, source, fp, path)
emit `}`
```

`writePlannedField` does field-resolve + complete in one pass,
emitting bytes as it goes:

- Scalar/Enum leaf: call resolver → `Serialize` → `leafEmitter` (or
  fallback) → append.
- Object: emit `{`, recurse into `fp.sub`, emit `}`.
- List: emit `[`, iterate items, comma-separate, recurse on each,
  emit `]`.
- Abstract: resolve concrete type, look up `fp.abstractAlternatives`,
  recurse.
- Null on nullable field: emit `null`.
- Null on non-nullable field: bubble up (see below).

### Null bubble-up state machine

The spec rule: if a non-nullable field resolves to null, the error
propagates upward until it hits a nullable parent, at which point
that parent becomes `null` and an error is recorded. This is the one
correctness hazard of append-mode — you can't un-write bytes already
appended to dst.

Approach: each object level writes into a **local sub-buffer**, not
straight into the parent. When the sub-buffer's walk completes
without a non-null bubble, the parent splices it in. When the walk
hits a non-null violation, the parent decides:

- Parent itself is nullable → discard sub-buffer, emit `null`, append
  the error to `eCtx.Errors`.
- Parent is non-null → propagate further up the same way.

Sub-buffers are `*[]byte` from a pool, length-reset on release. The
allocation cost is one buffer per object level deep — bounded by
query depth, not by field count. For typical queries (depth 3-5),
that's a handful of small slices, often satisfied from the pool's
warm side.

This is the **only** point in the design where naive append loses to
the map-tree path; everything else is strict win. The sub-buffer
cost is small (depth-bounded) and the alternative (defer-write the
entire tree) defeats the whole point.

### Errors

Collected in `eCtx.Errors` (existing slice on `executionContext`).
At the end of the top-level walk, append `,"errors":[...]` if
non-empty, marshaling each `gqlerrors.FormattedError` via the
existing serializer. Errors are rare on the hot path, so json.Marshal
here is fine.

The spec-level errors that today populate `Result.Errors` *before*
data walk begins (variable coercion failure, missing operation,
etc.) are returned as the second return value of
`ExecutePlanAppend` — the caller decides whether to emit a
`{"data":null,"errors":[...]}` envelope or surface the failure
some other way (e.g. HTTP 400).

---

## What stays slow / fallback

- **Thunked resolvers.** Append-mode can't unwind a `func() (interface{}, error)`
  return without dethunking eagerly. Detection: post-resolve
  `reflect.ValueOf(result).Kind() == reflect.Func` falls through to
  the same `completePlannedThunkValueCatchingError` path that
  `ExecutePlan` uses, then re-enters the append walker with the
  dethunked value. Cost: one extra function call per thunked field.
  Gateway never uses thunks; this is for adopter compatibility only.
- **Custom scalars without `AppendJSON`.** `Serialize` → `json.Marshal`
  → append. ~5-10 allocs per occurrence, same as today.
- **Introspection.** The `__schema` / `__type` fields hit the slow
  path. Plan-time check: if any field's selection set contains an
  introspection root, fall back to `ExecutePlan` + json.Marshal for
  the whole request. Rare; not worth optimizing.
- **Extensions hooks** (`handleExtensionsResolveFieldDidStart` etc.).
  Already opt-in (gated on `len(eCtx.Schema.extensions) > 0`). When
  extensions are registered, the per-field hook fires from
  append-mode the same way it fires from `ExecutePlan`. No extra
  cost on the no-extensions hot path.

---

## Phasing

### Phase 1 — Append-mode walker + leaf emitters

**Done.**
- [x] Plan-time `responseKeyJSON` + `leafEmitter` selection.
  `fieldPlan` gained `responseKeyJSON` / `leafEmitter` / `leafType`,
  populated in `collectInto` via `encodeResponseKeyJSON` and
  `pickLeafEmitter`. Built-in scalar emitters live in
  `scalars.go` (`appendIntJSON`, `appendFloatJSON`,
  `appendStringJSON`, `appendBoolJSON`, `appendDateTimeJSON`) plus
  shared `appendJSONString` (HTML-safe + U+2028/U+2029 escaping).
- [x] `Scalar.AppendJSON` hook. Optional field on `ScalarConfig`,
  surfaced as `(*Scalar).AppendJSONFn()`. Built-in scalars
  (`String`, `Int`, `Float`, `Boolean`, `ID`, `DateTime`) all set
  it; custom scalars opt in.
- [x] `writePlannedSelection` / `writePlannedField` walker.
  Mirrors the existing `executePlannedSelection` → `resolvePlannedField`
  → `completePlannedValue` chain across object / scalar / enum /
  list / abstract paths in `plan.go`.
- [x] Null bubble-up via length-rollback + panic propagation
  (deviated from sub-buffer pool — see Decisions log entry).
- [x] `ExecutePlanAppend` public function. Emits the
  `{"data":...,"errors":[...]}` envelope, returns spec-level errors
  (variable coercion etc.) as the second slice.
- [x] Test suite parity. `plan_append_test.go` covers scalars,
  nested objects, lists, non-null bubble-up at field + list-item
  level, resolver panics, directives (literal + variable), enums,
  interfaces, and variables; each case asserts JSON-decoded
  equivalence to `ExecutePlan`. One byte-shape spot check guards
  the envelope literal.
- [x] Bench parity. `BenchmarkPlannedAppend_*` siblings of all
  `BenchmarkPlannedExecute_*` cases.

**Headline numbers** (AMD Ryzen 9 3900X, single-threaded):

| Bench | ns/op Δ | B/op Δ | allocs/op Δ |
|---|---|---|---|
| WideQuery_100_10 | −28 % | −54 % | −22 % |
| WideQuery_100_10_Varied | −22 % | −39 % | −20 % |
| WideQuery_100_10_StaticArg | −23 % | −39 % | −20 % |
| ListQuery_1K | −42 % | −64 % | −36 % |

Bigger wins on list-heavy queries match the prediction: the map
tree's per-row scalar boxing dominates there.

**Followups.**
- Float/Int emitters currently use `'f'`/`'g'` heuristics that
  match `encoding/json` for typical magnitudes; tests asserting
  byte equality with `json.Marshal` for edge magnitudes
  (1e-7, 1e21, denormals) would be cheap insurance.
- `DateTime`'s emitter pays a `t.MarshalText` allocation per call.
  Bench shows it doesn't dominate; tracked as investigation-backlog
  item #4.
- Walker dethunks resolver thunks eagerly (single function call,
  per the plan). Workloads with parallel-thunk-friendly resolvers
  should stay on `ExecutePlan`. Documented in `ExecutePlanAppend`'s
  godoc.

### Phase 2 — Kill ResolveInfo heap escape + walker cleanup

Originally scoped as `ResolveParams` + args-map pools; investigation
flipped the priorities. `ResolveParams` is value-passed (no heap
alloc to recover), and the args map can't be safely pooled without a
contract change ("resolver may retain p.Args"). The actual hot alloc
turned out to be `ResolveInfo`: writePlannedField took `&info` to
hand to the extensions hook, which moved it to the heap on the
common no-extensions path too. That single fix reclaimed more allocs
than the original Phase 2 scope projected.

**Done.**
- [x] **Hoist the extensions `&info` into a slow-path branch with
  its own `infoForExt` copy.** `info` on the no-extension hot path
  now stays on the stack. ~1000 allocs × 200 KB reclaimed on the
  wide bench.
- [x] **Replace the per-field `defer func()` closure with a named
  `recoverPlannedField` / `recoverCompleteValue` taking pointers to
  the named returns.** Open-coded defer; cleaner read of the
  control flow. (Alloc-neutral in practice — Go 1.14+ open-coded
  the closure too — but easier to follow.)

**Headline numbers** (vs `ExecutePlan` map-tree baseline, same
hardware as Phase 1):

| Bench | ns/op Δ | B/op Δ | allocs/op Δ |
|---|---|---|---|
| WideQuery_100_10 | −45 % | −86 % | −37 % |
| WideQuery_100_10_Varied | −34 % | −61 % | −33 % |
| WideQuery_100_10_StaticArg | −33 % | −61 % | −33 % |
| ListQuery_1K | −58 % | −88 % | −47 % |

### Phase 3 — Inline scalar fast path for built-ins

**Done.**
- [x] **Pointer-equality switch on `fp.leafType` in `writeCompleteLeafValue`.**
  When the resolver returned the canonical Go type (`string` for
  `String`/`ID`, `int` in `int32` range for `Int`, finite `float64`
  for `Float`, `bool` for `Boolean`), emit directly and skip the
  `Serialize` + `leafEmitter` round-trip. Spec-edge inputs
  (`*string`, `int64` overflow, NaN/Inf, non-canonical types) fall
  through to the generic path unchanged.

The win is concentrated on String: `coerceString(string)` was
running `fmt.Sprintf("%v", s)` per call — one alloc per string
field on the hot path, identical content out. Int/Float/Bool save
CPU only (one fewer virtual `Serialize` dispatch + one redundant
type-switch).

**Headline numbers** (vs `ExecutePlan` map-tree baseline, same
hardware as Phase 1):

| Bench | ns/op Δ | B/op Δ | allocs/op Δ |
|---|---|---|---|
| WideQuery_100_10 | −55 % | −87 % | −47 % |
| WideQuery_100_10_Varied | −43 % | −62 % | −42 % |
| WideQuery_100_10_StaticArg | −43 % | −62 % | −42 % |
| ListQuery_1K | −62 % | −88 % | −53 % |

Delta vs Phase 2 alone: ~10 % additional ns/op on the wide queries,
~5 % on the list query; ~10–15 % additional allocs/op everywhere.
Exceeded the predicted 5–10 % CPU because the String Sprintf alloc
was bigger than the alloc profile read suggested.

### Phase 4 — Lazy `ResponsePath` (default-on; `PreserveInfoPath` opt-out)

**Optimization policy:** new perf knobs ship **fast by default** with
an opt-out flag callers can flip when the underlying contract isn't
safe for their schema. `PreserveInfoPath` is the first knob in that
mold — see also `ConcurrentThunks` below.

**Done.**
- [x] **Depth-stack response path is the default** under
  `ExecutePlanAppend`. The walker pushes raw keys into `eCtx.pathBuf`
  and skips per-field `*ResponsePath` allocation; `ResolveInfo.Path`
  is left **nil** for every resolver call. Reclaims ~1 alloc per
  resolved field and per list item (~1011 of the original 3738 allocs
  on `BenchmarkPlannedAppend_WideQuery_100_10`, ~2000 on
  `ListQuery_1K`).
- [x] **`ExecuteParams.PreserveInfoPath bool`** disables the
  optimization and restores the original behavior — per-field
  `*ResponsePath` alloc, populated `info.Path`. Set this when the
  schema has resolvers that key DataLoader cache entries on
  `info.Path`, build tracing spans from it, or stitch federation
  refs through it.
- [x] **Error envelope `path` is spec-correct in both modes.**
  `eCtx.errorPathArray` snapshots `pathBuf` (single slice copy,
  depth-bounded) under the default path; under `PreserveInfoPath` it
  walks the linked-list `AsArray` as before. Bytes are identical.
- [x] **Parity coverage.** `runParity` cross-runs every existing test
  under both modes and asserts JSON equivalence with `ExecutePlan`.
  Dedicated tests pin the `info.Path` contract: nil by default,
  populated under `PreserveInfoPath`.

**Contract caveat.** Default behavior is a silent-nil for resolvers
reading `info.Path`. The trade-off is intentional — the alternative
(opt-in optimization) leaves the speed on the floor for every
adopter who doesn't hear about it. If a resolver nil-derefs after
upgrade, flip `PreserveInfoPath=true` and file an issue.

**Headline numbers** (vs `ExecutePlan` map-tree baseline; default
`PreserveInfoPath=false`):

| Bench | ns/op Δ | B/op Δ | allocs/op Δ |
|---|---|---|---|
| WideQuery_100_10 | −67 % | −90 % | −61 % |
| WideQuery_100_10_Varied | −45 % | −64 % | −55 % |
| WideQuery_100_10_StaticArg | −46 % | −64 % | −55 % |
| ListQuery_1K | −64 % | −91 % | −68 % |

`PreserveInfoPath=true` (opt-out) trades back ~25-30 % of the ns/op
gains and the reclaimed allocs to restore the `info.Path` contract.

**Followups / caveats.**
- The `if !eCtx.lazyPath { ... } else { ... }` branch at each push
  site is a one-instruction overhead; bench shows it's in the noise
  vs. either pure mode. If it ever shows up on a profile, the two
  modes can be split into separate walker functions.
- `eCtx.pathBuf` is one slice alloc per request (depth-bounded
  capacity). Could be pooled via `sync.Pool` for a tiny additional
  win; tracked in the investigation backlog.

### Phase 5 — Concurrent thunks (default-eager; `ConcurrentThunks` opt-out)

**Done.**
- [x] **`ExecuteParams.ConcurrentThunks bool`** routes
  `ExecutePlanAppend` through `ExecutePlan` + `json.Marshal`,
  restoring the documented breadth-first dethunk pass. Schemas
  whose resolvers return `func() (interface{}, error)` thunks that
  kick off goroutines (the `examples/concurrent-resolvers` pattern)
  must set this — otherwise the append-mode walker dethunks
  synchronously and the resolver-side goroutines run serially in
  practice (correct values, no parallelism).
- [x] **Default stays eager-dethunk.** Append-mode keeps the
  inline single-pass walk that drives Phases 1–4's wins. Schemas
  that don't use thunks for concurrency (the common case — and the
  gateway adopter pattern this work was sized against) pay nothing.

**Why delegation, not a parallel-aware walker.** A two-pass append
walker (collect resolved values + thunks into an intermediate tree,
breadth-first dethunk, then emit bytes) would preserve some of
Phases 1–4's wins for thunk users. Honest cost analysis: the
intermediate-tree allocs largely eat those wins, leaving append-mode
roughly on par with `ExecutePlan` for thunk-using schemas anyway.
Delegating is one screenful of code and ships the correct semantics
today; the heavier walker is on the table if a real workload shows
the delegated path is the bottleneck.

**Trade-off (vs. `ExecutePlan`).** `ConcurrentThunks=true` gives up
the append-mode wedge in exchange for thunk concurrency:

- Bytes go through `json.Marshal(Result.Data)` instead of the
  leaf-emitter fast path — same alloc/CPU profile as `ExecutePlan +
  graphql.Do`.
- Spec-level errors (variable-coercion failures, missing operation,
  etc.) land in the envelope's `errors` array instead of the
  second return value of `ExecutePlanAppend`. `ExecutePlan` merges
  the two categories into `Result.Errors` and we don't try to
  re-split them.

### Investigation backlog

Ranked by win-to-risk ratio. Pull from the top when there's appetite
for another round; each entry stands alone and can ship without the
others. Numbers below are post-Phase-4 alloc-profile shares on
`BenchmarkPlannedAppendLazy_WideQuery_100_10`.

1. **Args map pool (with contract change).** `make(map[string]interface{},
   ...)` and `map[string]interface{}{}` are still 1 alloc per field
   (~1000 allocs on the wide bench). A `sync.Pool` of args maps with a
   clean-and-return discipline reclaims that, *if* resolvers agree not
   to retain `p.Args` past the resolve call.

   **Risk:** breaks any resolver that stashes `p.Args` in a struct
   field. Mitigate by gating behind a per-schema flag (`PoolArgs
   bool`) or a `ResolveAppend`-only path (Phase 6).

2. **Plan-time partial-literal arg pre-coercion.** Today
   `planArguments` is all-or-nothing: any `$variable` reference
   forces full `getArgumentValues` at execute time. Pre-coercing the
   literal subset cuts per-request coercion work on mixed arg lists.
   Mostly helps multi-arg fields with one variable.

   **Risk:** low. Mechanical refactor of `planArguments`.

3. **Direct `DateTime` format into dst.** The current emitter calls
   `t.MarshalText` then `appendJSONString(string(buf))`, paying one
   allocation per `DateTime` field. A direct format-into-`dst` version
   trims it. Doesn't dominate any bench but cheap and adopter-friendly.

4. **Direct error-array emit on the envelope tail.** `json.Marshal(eCtx.Errors)`
   in `ExecutePlanAppend` is only hit when errors exist, so not on the
   hot path — but for error-heavy callers a direct
   `appendFormattedErrors` would skip another reflective walk.

5. **`sync.Pool` for `eCtx.pathBuf` under LazyPath.** One slice alloc
   per request amortizes to near-zero with pooling; depth-bounded
   capacity makes the pool's discipline trivial. Marginal win — the
   single-slice alloc is small — but cheap.

### Phase 6 — Resolver-side append API (~3-5 days)

Opt-in. Lets downstream resolvers write JSON directly to the buffer
instead of returning `interface{}`. Removes the last boxing step and
the type-switch in `leafEmitter` for fields whose resolvers can
serialize themselves.

**Todo.**
- [ ] **`FieldDefinition.ResolveAppend func(ResolveParams, []byte) ([]byte, error)`.**
  Optional alongside `Resolve`. When set, the walker calls it instead
  of `Resolve` + emit. Existing `Resolve` remains the documented
  path; `ResolveAppend` is the perf opt-in. ~1 day.
- [ ] **Wire into walker.** Branch in `writePlannedField`. ~0.5 day.
- [ ] **Adopter example.** Gateway-side dispatcher rewrite (separate
  PR in the gateway repo, gated on this landing). Not in this repo's
  scope.

**Expected:** ~30-50 additional allocs reclaimed when the adopter
goes all-in. Compound with Phases 1–4 to reach the projected 3-4×
end-to-end wedge cited in the gateway's perf docs.

---

## Risks & open questions

1. **Spec conformance on null bubble-up.** Append-mode's sub-buffer
   approach must produce byte-equivalent (after JSON normalization)
   responses to `ExecutePlan` for every spec scenario. Mitigation:
   the Phase 1 test suite parity item explicitly cross-runs both
   executors against the existing suite. Treat any diff as a Phase 1
   blocker.
2. **Map-ordering observability.** `ExecutePlan` returns
   `map[string]interface{}`; Go map iteration is randomized, so today
   the final JSON order is non-deterministic. `ExecutePlanAppend`
   emits in `selectionPlan.fields` order — deterministic. Adopters
   that depended on the random order (unlikely but possible) see a
   behavior change. Document; not a blocker.
3. **`json.Marshal` compatibility for custom scalars.** Phase 1's
   fallback path serializes custom scalars via `json.Marshal(result)`.
   If a custom scalar's Serialize returns a value that isn't
   round-trippable via `json.Marshal` (unlikely — Serialize is
   spec'd to return JSON-compatible values), this surfaces as
   subtly-wrong output. Open: do we want `AppendJSON` to be
   required for custom scalars, or keep the fallback? Settled
   answer: keep the fallback for adopter ergonomics; document the
   perf cliff.
4. **`io.Writer` vs `[]byte` as the primary public API.** Settled
   on `[]byte`-append as primary because it's the simplest target
   (no error path, no flushing, no partial-write logic), and the
   `io.Writer` wrapper is trivial. Adopters wanting streaming write
   `bytes.Buffer` and `Write` at the end; adopters wanting
   `http.ResponseWriter` write to a pooled buffer then one
   `w.Write(buf)`.
5. **`ResolveAppend` API stability** (Phase 6). Once exposed, hard
   to remove. Mitigation: keep it experimental — package doc warns
   that the signature may change pre-1.0 of this fork; existing
   `Resolve` remains the documented stable surface.
6. **Plan invalidation.** Plan-time `responseKeyJSON` /
   `leafEmitter` selection depends on the schema. Schema changes →
   plan cache eviction (already in place). Custom scalar
   `AppendJSON` mutation post-schema-build is **not** supported;
   plans capture the function pointer at PlanQuery time.

---

## Decisions log

| Decision | Rationale |
|---|---|
| **Sibling `ExecutePlanAppend`, not replacement** | `ExecutePlan` has existing callers; append-mode opt-in keeps the upgrade path incremental and the test surface bounded. |
| **`[]byte`-append as primary API; `io.Writer` derived** | Simplest target; no flushing or partial-write logic. Streaming wraps it trivially. |
| **Length-rollback (single dst) + panic propagation for null bubble-up — revised from sub-buffer pool** | Sub-buffers add `sync.Pool` Get/Put per object level and a splice on success. Single-dst with `out = out[:entryLen]` on the deferred recover gives the same correctness: bytes past the rollback point are invisible to subsequent appends. Caller's dst keeps the grown capacity for the next request; no per-level pool churn. Decided after implementation showed the simpler shape matched the existing `completePlannedValueCatchingError` pattern almost line-for-line. |
| **Built-in scalars get default `AppendJSON`; custom scalars opt in via the hook; fallback is `Serialize` + `json.Marshal`** | Adopter ergonomics: existing code works unchanged; perf opt-in is one func per custom scalar. |
| **Plan captures `AppendJSON` at PlanQuery time** | Plans are immutable post-build; the cache invalidates on schema change. Mutating `AppendJSON` after a plan is built is unsupported (would yield mixed-source results from cached plans). |
| **Introspection falls back to `ExecutePlan`** | Rare; not worth the special-casing. Plan-time detection routes the whole request to the slow path. |
| **`map[string]interface{}` map ordering — non-deterministic today, deterministic in append-mode** | Documenting the behavior change; not gating. Random ordering was an artifact, not a feature. |

---

## Reference

### Where the work lands

| File | Phase | Change | Status |
|---|---|---|---|
| `plan.go` | 1 | Add `responseKeyJSON` / `leafEmitter` / `leafType` to `fieldPlan`; populate in `collectInto` via `encodeResponseKeyJSON` + `pickLeafEmitter`. | landed |
| `plan.go` | 1 | New `ExecutePlanAppend` + `writePlannedSelection` + `writePlannedField` + completion mirrors. | landed |
| `definition.go` | 1 | Add `AppendJSON` to `ScalarConfig`; expose via `(*Scalar).AppendJSONFn()`. | landed |
| `scalars.go` | 1 | Default `AppendJSON` for built-in scalars; shared `appendJSONString` helper. | landed |
| `plan_bench_test.go` | 1 | `BenchmarkPlannedAppend_*` siblings. | landed |
| `plan_append_test.go` | 1 | Cross-run parity helper + coverage matrix. | landed |
| `plan.go` | 2 | Hoist `&info` into the extensions slow-path branch; open-code the per-field recover (was scoped as `ResolveParams` + args-map pools; pivoted — see Phase 2). | landed |
| `plan.go` | 3 | Inline canonical-Go-type fast path in `writeCompleteLeafValue` for `String` / `ID` / `Int` / `Float` / `Boolean`. | landed |
| `executor.go` | 4 | Add `ExecuteParams.PreserveInfoPath` (opt-out); add `lazyPath` / `pathBuf` to `executionContext`; add `popPath` / `errorPathArray` helpers; route `handleFieldError` through `errorPathArray`. | landed |
| `plan.go` | 4 | Branch on `eCtx.lazyPath` at `writePlannedField` + `writeCompleteListValue` push sites; thread `pathEntry` through `recoverPlannedField` / `recoverCompleteValue` for the unwind. Default-on; `PreserveInfoPath=true` restores legacy behavior. | landed |
| `plan_append_test.go` | 4 | `runParity` cross-runs `PreserveInfoPath=true`; dedicated tests pin `info.Path` contract and error-location parity. | landed |
| `plan_bench_test.go` | 4 | `BenchmarkPlannedAppendEager_*` siblings (PreserveInfoPath=true) measure opt-out cost. | landed |
| `executor.go` | 5 | Add `ExecuteParams.ConcurrentThunks` (opt-out). | landed |
| `plan.go` | 5 | `executePlanAppendViaResult` delegate that runs `ExecutePlan` + `json.Marshal` when the caller opts back into the breadth-first dethunk pass. | landed |
| `plan_append_test.go` | 5 | `TestAppendConcurrentThunks` exercises the thunk path and cross-checks default-mode (eager) parity. | landed |
| `definition.go` | 6 | Add `ResolveAppend` to `FieldDefinition` (`Field`). | pending |
| `plan.go` | 6 | Walker branches on `ResolveAppend` presence. | pending |

### Append-mode invariants (for reviewers)

1. **Field write order** matches `selectionPlan.fields` order.
2. **Skipped fields emit nothing** — no leading comma, no key, no
   value. The next non-skipped field writes its own leading comma if
   not the first.
3. **Sub-buffers are length-reset on release**, not capacity-reset.
   Caller mutates via `*buf = (*buf)[:0]`.
4. **Errors never abort the walk**; they accumulate in `eCtx.Errors`.
   Null bubble-up may discard a sub-buffer but the parent walker
   continues with the next field.
5. **`AppendJSON` may not retain `dst`** — it must return the new
   slice and never write to `dst[:0]` underlying-array directly
   after the call returns.
