# Changesets

Every change to a package under `packages/` needs a changeset: run `pnpm changeset`, pick the
package and the bump, describe the change. Merging to `main` makes the release workflow bump the
version, write the changelog and tag the Go module (`packages/<dir>/vX.Y.Z`).

A README-only change needs no changeset.
