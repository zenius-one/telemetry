# Repository instructions

This repository's process lives in [`.ai/`](.ai/README.md); read it before changing anything.

- Committing and changesets: [`.ai/skills/commit-strategy/SKILL.md`](.ai/skills/commit-strategy/SKILL.md)
- Issues, branches and PRs: [`.ai/skills/pr-strategy/SKILL.md`](.ai/skills/pr-strategy/SKILL.md)
- Dependency map: [`.ai/references/dependency-graph/README.md`](.ai/references/dependency-graph/README.md)

Core rules: one issue per PR; packages stay generic (no product concepts); core modules take no
observability backend; a module change needs a hand-written changeset; run
`pnpm turbo run build lint check-types test`.
