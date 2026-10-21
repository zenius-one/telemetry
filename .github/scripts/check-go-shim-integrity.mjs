#!/usr/bin/env node
// Guards the one invariant workflows/publish-packages.yml's reconcile mode
// depends on: a Go module's version-tracking shim (packages/<dir>/package.json,
// see .github/README.md) is an append-only ledger of "what version should be
// published." Reconcile mode trusts the shim's committed version as source
// of truth and diffs it against actual tags/releases — so a shim that gets
// deleted or hand-rolled backwards would make reconcile either lose track of
// a module entirely or attempt to re-publish an already-superseded version.
//
// Checked against the PR's base ref, for every shim that already existed
// there:
//   - it must still exist on the PR branch (no silent deletion)
//   - its version must not be lower than the base branch's version
//
// A legitimate module removal is still possible — delete the shim AND drop
// the module from publish-go-packages.config.json's "include" in the same
// PR — sync-changeset-ignore.mjs's check already forces that pairing to be
// intentional, this script just refuses to let the shim vanish silently on
// its own.
//
// Usage:
//   node check-go-shim-integrity.mjs <base-ref>   # e.g. origin/main

import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";

const REPO_ROOT = process.cwd();
const [, , baseRef] = process.argv;

function fail(message) {
  console.error(`::error::${message}`);
  process.exit(1);
}

if (!baseRef) {
  fail("Usage: check-go-shim-integrity.mjs <base-ref>");
}

// Minimal semver comparator — X.Y.Z, no prerelease/build metadata handling.
// Every version this pipeline ever writes comes from `changeset version`,
// which only ever emits plain X.Y.Z, so that's all this needs to support.
function parseVersion(version) {
  const match = /^(\d+)\.(\d+)\.(\d+)/.exec(version ?? "");
  if (!match) return null;
  return match.slice(1, 4).map(Number);
}

function isLower(a, b) {
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) return a[i] < b[i];
  }
  return false;
}

function listBaseShimPaths() {
  let raw;
  try {
    raw = execFileSync(
      "git",
      ["ls-tree", "-r", "--name-only", baseRef, "--", "packages/"],
      { encoding: "utf8", cwd: REPO_ROOT },
    );
  } catch (err) {
    fail(`Failed to list ${baseRef}'s packages/ tree: ${err.message}`);
  }
  return raw
    .split("\n")
    .filter((line) => line.endsWith("package.json"))
    .map((line) => path.dirname(line));
}

function readAt(ref, relPath) {
  try {
    return execFileSync("git", ["show", `${ref}:${relPath}`], {
      encoding: "utf8",
      cwd: REPO_ROOT,
      stdio: ["ignore", "pipe", "ignore"], // used to probe existence; a miss is expected, not an error
    });
  } catch {
    return null;
  }
}

const baseShimDirs = listBaseShimPaths().filter((dir) => {
  // A shim is identified the same way sync-changeset-ignore.mjs does: a
  // package.json with a sibling go.mod, on the base ref.
  return readAt(baseRef, `${dir}/go.mod`) !== null;
});

let failures = 0;

for (const dir of baseShimDirs) {
  const shimPath = `${dir}/package.json`;

  if (!existsSync(path.join(REPO_ROOT, shimPath))) {
    console.error(
      `::error::${shimPath} was deleted. It's the version ledger for a Go ` +
        `module publish-packages.yml's reconcile mode relies on — if this ` +
        `module is being intentionally retired, also remove "${dir}" from ` +
        `.github/publish-go-packages.config.json's "include" in this PR.`,
    );
    failures++;
    continue;
  }

  const baseRaw = readAt(baseRef, shimPath);
  const baseVersion = parseVersion(JSON.parse(baseRaw).version);
  const headVersion = parseVersion(
    JSON.parse(readFileSync(path.join(REPO_ROOT, shimPath), "utf8")).version,
  );

  if (!baseVersion || !headVersion) continue; // unparseable — not this check's job

  if (isLower(headVersion, baseVersion)) {
    console.error(
      `::error::${shimPath} version went backwards (${baseVersion.join(".")} -> ` +
        `${headVersion.join(".")}). This shim is an append-only ledger — its ` +
        "version must only ever move forward via `changeset version`.",
    );
    failures++;
  }
}

if (failures > 0) {
  fail(`${failures} Go module shim integrity violation(s) found.`);
}

console.log(`${baseShimDirs.length} Go module shim(s) checked, all intact.`);
