#!/usr/bin/env node
// After workflows/publish-packages.yml's publish-go job tags a Go module (e.g.
// packages/readthrough/v0.2.0), every other go.work module that imports it is still pinned to the
// OLD version in its own go.mod (packages/readthrough/prom requires packages/readthrough). Locally
// and in CI that is invisible: go.work's `use (...)` directives make Go resolve those imports
// against workspace source regardless of what go.mod says. It stops being invisible for a
// consumer, or any GOWORK=off build, which resolves strictly from the pin.
//
// This script is the mechanical half of closing that gap: given the go-matrix this run's release
// job just bumped, find every other go.work module that requires one of those modules, and
// `go get` + `go mod tidy` each one onto the new tag. The workflow step calling this handles
// `go work sync`, build/vet verification and the commit; this script only touches go.mod/go.sum
// through the go tool, never by hand.
//
// Usage:
//   GO_MATRIX='[{"module":"...","dir":"...","version":"0.2.0","tag":"..."}]' \
//     node bump-go-consumers.mjs

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";

const REPO_ROOT = process.cwd();

function fail(message) {
  console.error(`::error::${message}`);
  process.exit(1);
}

let goMatrix;
try {
  goMatrix = JSON.parse(process.env.GO_MATRIX ?? "[]");
} catch (err) {
  fail(`GO_MATRIX is not valid JSON: ${err.message}`);
}

if (goMatrix.length === 0) {
  console.log("No bumped Go modules — nothing to do.");
  process.exit(0);
}

// Parse go.work's `use (...)` block for the full set of workspace module directories, the same
// set go.work itself resolves against. Paths there are written `./packages/x`; the matrix uses
// `packages/x`, so normalise both to the same form before comparing.
const goWork = readFileSync(path.join(REPO_ROOT, "go.work"), "utf8");
const useBlock = goWork.match(/use\s*\(([^)]*)\)/s);
if (!useBlock) fail("Couldn't find a `use (...)` block in go.work.");
const workspaceDirs = useBlock[1]
  .split("\n")
  .map((l) => l.trim())
  .filter(Boolean)
  .map((d) => path.posix.normalize(d));

function escapeRegExp(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// consumers: dir -> [{ module, version }]
const consumers = new Map();

for (const bumped of goMatrix) {
  const bumpedDir = path.posix.normalize(bumped.dir);
  const modulePattern = new RegExp(
    `(^|\\s)${escapeRegExp(bumped.module)}(\\s+v[0-9])`,
    "m",
  );
  for (const dir of workspaceDirs) {
    if (dir === bumpedDir) continue; // a module never depends on itself
    let goModContents;
    try {
      goModContents = readFileSync(path.join(REPO_ROOT, dir, "go.mod"), "utf8");
    } catch {
      continue; // no go.mod at this workspace dir — skip
    }
    if (!modulePattern.test(goModContents)) continue;

    if (!consumers.has(dir)) consumers.set(dir, []);
    consumers.get(dir).push({ module: bumped.module, version: bumped.version });
  }
}

if (consumers.size === 0) {
  console.log("No internal consumers of the bumped module(s) — nothing to do.");
  process.exit(0);
}

for (const [dir, bumps] of consumers) {
  console.log(`\n== ${dir} ==`);
  // One `go get` carrying every bumped module@version this dir needs, not one call per module:
  // `go get modA@v1` alone still resolves the whole build graph with this dir's other
  // requirements as currently pinned, so sequential calls can trip over an inconsistent
  // intermediate state.
  const targets = bumps.map(({ module, version }) => `${module}@v${version}`);
  const cwd = path.join(REPO_ROOT, dir);
  // GOWORK=off: bump the pin as a consumer would see it, not through the workspace.
  const env = { ...process.env, GOWORK: "off" };
  console.log(`go get ${targets.join(" ")}`);
  execFileSync("go", ["get", ...targets], {
    cwd,
    env,
    encoding: "utf8",
    stdio: "pipe",
  });
  console.log("go mod tidy");
  execFileSync("go", ["mod", "tidy"], {
    cwd,
    env,
    encoding: "utf8",
    stdio: "pipe",
  });
}

console.log(`\nUpdated ${consumers.size} consumer module(s).`);
