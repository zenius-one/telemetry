# Copilot instructions

This repository is a pnpm + Turborepo + Changesets monorepo of generic telemetry packages. Process
lives in `.ai/`; do not duplicate it here.

## Layout

- `packages/readthrough/`: Go core, read-through cache protection over `cachestore.CacheStore`.
  **No observability backend.**
- `packages/readthrough/prom/`: separate Go module with the Prometheus `Observer`.
- Each Go module has a `package.json` shim (version tracking and Turborepo scripts only).

## Always

- Keep packages generic: no product concepts (tenant, org, plane, PII policy) in a package API.
- Keep Prometheus/OTel out of core modules; add an adapter module instead.
- Treat cache errors as misses; never let a cache failure fail a request; hash anything secret or
  personal before using it in a key; apply TTL jitter inside the package.
- Update the package README and tests with any behaviour change.
- Add a changeset (`.changeset/<slug>.md`, package name from the shim's `package.json`) for any
  change to a published module. A README-only change needs none.
- Run `pnpm turbo run build lint check-types test` before proposing a change.
- Use conventional commit subjects (`feat(readthrough): ...`).
- One issue per PR; branch `<type>/<issue-number>-short-description`; exactly one `Closes #n`;
  fill every section of `.github/PULL_REQUEST_TEMPLATE.md`.

## Never

- Hand-edit `.changeset/config.json`'s `ignore` list (generated).
- Commit a release tag or version bump by hand.
- Push to `main`.

Procedures: `.ai/skills/commit-strategy/SKILL.md`, `.ai/skills/pr-strategy/SKILL.md`.
Dependency map: `.ai/references/dependency-graph/README.md`.
