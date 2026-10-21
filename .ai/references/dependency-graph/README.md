# Dependency graph

The map `skills/commit-strategy` operates on. Update it whenever a package is added, moved or
gains a dependency.

## Packages

Each package's README is the source of truth for its behaviour; behaviour changes ship with tests
in the same change.

```
packages/readthrough              Go core: read-through cache protection, Observer interface
  `-- packages/readthrough/prom   Go: Prometheus Observer (requires the core module)
```

## Go modules

| Module | Path | Requires |
| --- | --- | --- |
| readthrough | `packages/readthrough` | `github.com/zenius-one/cachestore/packages/cachestore-go`, `golang.org/x/sync` (no observability backend, deliberately) |
| readthrough-prom | `packages/readthrough/prom` | readthrough, `github.com/prometheus/client_golang` |

External contract: `readthrough` talks to any `cachestore.CacheStore`. The Redis implementation
(`cachestore-go/redis`, package `cacheredis`) is chosen by the consuming service, never by this
repository's non-test code.

- A dependency edge means: change the upstream first, release it, pin the new version downstream.
- The root `go.work` resolves the modules locally. `GOWORK=off` is how a consumer sees them and
  how CI builds before tagging; it needs the required version to exist as a tag.
- The core module must not gain an observability dependency (Prometheus, OTel): add an adapter
  module next to `prom` instead.
- The prom module's `go.mod` pins a released core version. Bump it deliberately (`GOWORK=off go
  get github.com/zenius-one/telemetry/packages/readthrough@vX.Y.Z`) after core releases a change
  prom needs; Go will not resolve a required version that has no tag, even in a workspace.

## Release graph

Each Go module has a `package.json` next to its `go.mod`. It is a **version-tracking shim** (never
published to npm) so Changesets can bump it and Turborepo can run its scripts.

| Shim name | Module | Released when |
| --- | --- | --- |
| `@zenius-one/telemetry-readthrough-go` | core | listed in `.github/publish-go-packages.config.json` and bumped by a changeset |
| `@zenius-one/telemetry-readthrough-prom-go` | prom | listed in `.github/publish-go-packages.config.json` and bumped by a changeset |

Tags are `packages/<dir>/vX.Y.Z` (`packages/readthrough/prom/vX.Y.Z` for the nested module):
the Go toolchain resolves a module in a subdirectory by its directory-prefixed tag.

Eligibility lives only in `.github/publish-packages.config.json` and
`.github/publish-go-packages.config.json`; `.changeset/config.json`'s `ignore` list is generated
from them by `.github/scripts/sync-changeset-ignore.mjs`.
