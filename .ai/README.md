# .ai

Reference material and skills for AI agents working in this repository: Claude Code, GitHub
Copilot and Cursor all read from here. Content is process and documentation, never built,
deployed or imported by any package.

```
.ai/
  references/
    dependency-graph/        What depends on what: the Go module graph (readthrough <-
                             readthrough/prom). The data that skills/commit-strategy operates on.
  skills/
    commit-strategy/         How to sequence commits so every one builds, and how and when to
                             write a changeset.
    pr-strategy/             How to take one issue to one PR: issue, branch, commits, changeset,
                             PR body.
```

## How each tool finds this

| Tool | Entry point | What it contains |
| --- | --- | --- |
| Claude Code | `CLAUDE.md`, `.claude/skills/*/SKILL.md` | project summary, and thin wrappers that point at `.ai/skills/*` |
| GitHub Copilot | `.github/copilot-instructions.md` | the same rules, condensed |
| Cursor | `.cursor/rules/*.mdc` | always-on project rules plus a rule scoped to `packages/**` and `.changeset/**` |
| Other agents | `AGENTS.md` | pointer to this directory |

The skills under `.ai/skills/` are the single source of truth. The tool-specific files stay short
and point here; do not copy procedure into them, or they drift.

## Adding to this directory

- **New skill:** a subdirectory under `skills/` with a `SKILL.md` (frontmatter `name` and
  `description`), then a `.claude/skills/<name>/SKILL.md` wrapper and a line in each tool entry
  point above if it should be discoverable.
- **New reference:** a subdirectory under `references/` with its own `README.md`.
- Update the tree above.
