# Contributing

## Principles

- **All work starts from an issue**, created from a template (blank issues are disabled).
- **One issue = one unit of work, and one pull request = one issue.**
- If a change cannot be tied to an issue, it does not get merged.

## Branches

`<type>/<issue-number>-short-description`, for example `feature/12-ttl-jitter` or
`fix/34-memory-store-eviction`. Branch names are informational; the PR body links the issue.

## Pull requests

Every pull request must:

1. link exactly one issue with a closing keyword (`Closes #<n>`, `Fixes #<n>` or `Resolves #<n>`);
2. follow the PR template, including an honest "How was this tested?" and a non-empty
   "Risk & Rollback";
3. include a changeset when it changes a published module (a README-only change needs none);
4. pass all checks.

## Behaviour changes

Each package's README is its behaviour contract. A change to behaviour changes the README and its
tests in the same PR (or a stack of PRs that merge in order, each its own issue).

## Local development

```sh
pnpm install
pnpm turbo run build lint check-types test
```

Go modules resolve each other through the root `go.work`.

## Releases

Merging to `main` with a changeset runs `publish-packages.yml`: version bump, changelog, tag and
GitHub release. See [`.github/README.md`](.github/README.md).

## AI assistants

Claude Code, GitHub Copilot and Cursor are configured to follow the process in
[`.ai/`](.ai/README.md). The same rules apply to their changes as to anyone's.
