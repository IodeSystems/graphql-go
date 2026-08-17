# Upstream sync ledger

This fork tracks `graphql-go/graphql` (remote `upstream`). This file records
how far upstream has been absorbed and what was deliberately left behind.

**Last absorbed:** `6acef3563ff762c0f5cd14b759ed2cca405ac8fa` (2026-06-22,
"Merge pull request #753 from graphql-go/ast-definitions-unit-tests")

## Sync procedure

```
git fetch upstream
git log --oneline --no-merges HEAD..upstream/master   # what's new
```

For each new commit, cherry-pick it (`git cherry-pick -n <sha>`), resolve
against this fork's divergence, and commit with the upstream SHA in a
`(cherry picked from commit <sha>)` trailer so provenance survives the
adaptation. Anything intentionally not taken goes in the skip list below,
with a reason — otherwise the next sync silently re-decides it.

Then close the gap so `git rev-list --count HEAD..upstream/master` stays a
real signal:

- If nothing conflicts, `git merge upstream/master`.
- If the content is already absorbed via cherry-pick, `git merge -s ours
  upstream/master`. Update the "Last absorbed" SHA above in the same pass.

### Why `-s ours` needs this file

`-s ours` records upstream as an ancestor while taking none of its content.
That is accurate when the content arrived by cherry-pick, but it also means
anything skipped can never return through a later merge — git will consider
it already merged. The skip list is the only remaining record. Keep it
current.

## Fork divergence (what makes cherry-picks conflict)

- Import paths: `github.com/graphql-go/graphql` → `github.com/IodeSystems/graphql-go`.
  Every upstream commit touching an import block conflicts on this alone.
- `plan.go` / `executor.go` carry the append-mode pipeline (`PlanQuery`,
  `ExecutePlan`, `ExecutePlanAppend`, `ResolveAppend`). See `docs/plan.md`.
- `ResolveInfo.Path` and the `ResponsePath` type were removed (682320e), so
  upstream code passing a `path` argument needs it dropped.
- `DefaultResolveFn` resolves struct fields through the cached
  `resolveDefaultStructField` rather than upstream's per-call field scan.
- Introspection returns `__schema.types` and `__type.fields` in sorted order.

## Absorbed

| upstream | here | note |
|---|---|---|
| `526d0f9` overlapping-fields cyclic-fragment guard | `49e909d` | fixed here independently; diff vs upstream is import paths only |
| `08bddaa` lazy abstract-field planning | `12e9272` | wired through both walkers, not just the map-tree one; `path` arg dropped; added `TestAppendParity_Union` |
| `3844c38` printer nil operation name | `355ed1e` | tests absorbed verbatim |
| `d617417` executor nil `conditionalType` guard | `8150715` | tests absorbed; two `handleFieldError` calls lost their `*ResponsePath` arg |
| `ba47337` definition coverage tests | `ae8a0c1` | carries the dead `if err != nil` removal in `defineInterfaces` |
| `45011ef` definition follow-up tests | `ffc9f3d` | clean |
| `5d67513` introspection coverage tests | `19044a7` | clean |
| `4593044`, `b2ab666` language/ast coverage tests | `3cee1f5` | clean |
| `7304e7d` drops unused `customMap` type | `b426a2b` | clean; landed upstream after the commit `executor_internal_test.go` came from, so the absorbed copy still had it |

## Net-zero upstream

- `e55663f` removed error propagation in `parseDocument` / `parseOperationDefinition`;
  `de087e8` put it back. Nothing to take.

## Deliberately skipped

- **`d1712d3`** — drops the two `!valueVal.IsValid()` guards in
  `introspection.go`'s `astFromValue`. Upstream calls them unreachable behind
  the preceding `isNullish(value)` check. Removing them buys nothing and the
  second one guards a `.Elem()` on a possibly-nil pointer, so dropping it
  trades a live nil-check for a panic if the reachability claim is ever wrong.
