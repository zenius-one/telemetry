#!/usr/bin/env node
// Snapshots and diffs the versions of every Changesets-managed package
// (npm packages and Go module shims alike — see sync-changeset-ignore.mjs
// for what "managed" means) around a `changeset version` run, and — in
// diff mode — classifies what changed into an npm matrix and a Go matrix
// for workflows/publish-packages.yml.
//
// There's no separate "did this package change" detection step anymore
// (unlike the old svu/VERSION-file era): Changesets IS the change
// detection. A package only has a version diff here if a changeset
// actually targeted it.
//
// Usage:
//   node resolve-changeset-release.mjs snapshot <path>
//     Writes {name: version} for every managed package to <path>.
//   node resolve-changeset-release.mjs diff <path>
//     Re-lists managed packages, compares against the snapshot at <path>,
//     and writes GitHub Actions outputs: npm-matrix, go-matrix,
//     has-npm-targets, has-go-targets.
//   node resolve-changeset-release.mjs reconcile
//     Recovery path for workflow_dispatch (mode: reconcile) — for when a
//     `diff`-driven run's version-bump commit landed but a publish job
//     failed downstream (see .github/README.md), so a later push-triggered
//     run has no pending changeset to re-diff against. Trusts each managed
//     package's currently-committed version as source of truth (npm
//     package.json / Go module shim alike — both are only ever bumped
//     forward by `changeset version`, and Go shims are guarded against
//     deletion/regression by check-go-shim-integrity.mjs) and compares it
//     against what's actually published — npm registry + GitHub Release for
//     npm packages, git tag + GitHub Release for Go modules — rather than
//     against a before/after snapshot. A package already fully published at
//     its current version is left alone; this never looks at version
//     history, so it naturally only ever targets the latest committed
//     version per package, not a backlog of every version skipped along the
//     way. Writes the same GitHub Actions outputs as `diff`.
//     Needs GH_TOKEN (for `gh release view`) and, for npm packages, a
//     registry-authenticated .npmrc (same as the publish-npm job's own
//     "Configure npm registry" step) on PATH already.

import { execFileSync } from "node:child_process";
import { appendFileSync, existsSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";

const REPO_ROOT = process.cwd();
const [, , mode, snapshotPath] = process.argv;

function fail(message) {
  console.error(`::error::${message}`);
  process.exit(1);
}

if (!["snapshot", "diff", "reconcile"].includes(mode)) {
  fail("Usage: resolve-changeset-release.mjs <snapshot|diff> <path> | reconcile");
}
if (["snapshot", "diff"].includes(mode) && !snapshotPath) {
  fail("Usage: resolve-changeset-release.mjs <snapshot|diff> <path>");
}

function readJson(relPath) {
  try {
    return JSON.parse(readFileSync(path.join(REPO_ROOT, relPath), "utf8"));
  } catch (err) {
    fail(`Failed to read/parse ${relPath}: ${err.message}`);
  }
}

function globToRegExp(glob) {
  const escaped = glob.replace(/[.+^${}()|[\]\\]/g, "\\$&");
  const pattern = escaped.replace(/\*/g, ".*").replace(/\?/g, ".");
  return new RegExp(`^${pattern}$`);
}

function matchesAny(value, globs) {
  return globs.some((glob) => globToRegExp(glob).test(value));
}

function writeOutput(name, value) {
  const outputFile = process.env.GITHUB_OUTPUT;
  if (!outputFile) {
    console.log(`${name}=${value}`);
    return;
  }
  if (value.includes("\n")) {
    const delimiter = `EOF_${name}_${Date.now()}`;
    appendFileSync(outputFile, `${name}<<${delimiter}\n${value}\n${delimiter}\n`);
  } else {
    appendFileSync(outputFile, `${name}=${value}\n`);
  }
}

function listManagedPackages() {
  const npmConfig = readJson(".github/publish-packages.config.json");
  const goConfig = readJson(".github/publish-go-packages.config.json");

  let workspacePackages;
  try {
    const raw = execFileSync("pnpm", ["-r", "list", "--json", "--depth", "-1"], {
      encoding: "utf8",
      cwd: REPO_ROOT,
    });
    workspacePackages = JSON.parse(raw);
  } catch (err) {
    fail(`Failed to list pnpm workspace packages: ${err.message}`);
  }

  const managed = [];
  for (const pkg of workspacePackages) {
    if (!pkg.name || pkg.private === undefined) continue; // root project entry
    const relPath = path.relative(REPO_ROOT, pkg.path).split(path.sep).join("/");
    const isGoShim = existsSync(path.join(REPO_ROOT, relPath, "go.mod"));

    const eligible = isGoShim
      ? relPath.startsWith("packages/") &&
        matchesAny(relPath, goConfig.include ?? []) &&
        !matchesAny(relPath, goConfig.exclude ?? [])
      : matchesAny(relPath, npmConfig.include ?? []) &&
        !matchesAny(relPath, npmConfig.exclude ?? []) &&
        pkg.private !== true;

    if (eligible) managed.push({ name: pkg.name, path: relPath, isGoShim });
  }
  return managed;
}

if (mode === "snapshot") {
  const managed = listManagedPackages();
  const versions = {};
  for (const pkg of managed) {
    const pkgJson = readJson(`${pkg.path}/package.json`);
    versions[pkg.name] = pkgJson.version;
  }
  writeFileSync(path.resolve(REPO_ROOT, snapshotPath), JSON.stringify(versions, null, 2));
  console.log(`Snapshotted ${managed.length} managed package version(s) to ${snapshotPath}.`);
  process.exit(0);
}

function goModuleInfo(pkg, version) {
  const goModContents = readFileSync(path.join(REPO_ROOT, pkg.path, "go.mod"), "utf8");
  const moduleMatch = goModContents.match(/^module\s+(\S+)/m);
  if (!moduleMatch) {
    console.log(`::warning::${pkg.path}/go.mod has no "module" directive, skipping.`);
    return null;
  }
  return {
    module: moduleMatch[1],
    dir: pkg.path,
    version,
    tag: `${pkg.path}/v${version}`,
  };
}

if (mode === "diff") {
  let before;
  try {
    before = JSON.parse(readFileSync(path.resolve(REPO_ROOT, snapshotPath), "utf8"));
  } catch (err) {
    fail(`Failed to read snapshot ${snapshotPath}: ${err.message}`);
  }

  const managed = listManagedPackages();
  const npmMatrix = [];
  const goMatrix = [];

  for (const pkg of managed) {
    const pkgJson = readJson(`${pkg.path}/package.json`);
    const newVersion = pkgJson.version;
    const oldVersion = before[pkg.name];

    if (oldVersion === newVersion) continue; // untouched by this changeset version run

    if (pkg.isGoShim) {
      const info = goModuleInfo(pkg, newVersion);
      if (info) goMatrix.push(info);
    } else {
      npmMatrix.push({ name: pkg.name, path: pkg.path, version: newVersion });
    }
  }

  console.log(
    `Bumped: ${npmMatrix.length} npm package(s), ${goMatrix.length} Go module(s).`,
  );
  writeOutput("npm-matrix", JSON.stringify(npmMatrix));
  writeOutput("go-matrix", JSON.stringify(goMatrix));
  writeOutput("has-npm-targets", String(npmMatrix.length > 0));
  writeOutput("has-go-targets", String(goMatrix.length > 0));
  process.exit(0);
}

// mode === "reconcile"
//
// "Fully published" means both the artifact and its GitHub Release exist —
// checking only one would leave the other half of a partial failure (e.g.
// tag pushed but the GoReleaser release step failed) undetected.

function commandSucceeds(cmd, args) {
  try {
    execFileSync(cmd, args, { stdio: "ignore", cwd: REPO_ROOT });
    return true;
  } catch {
    return false;
  }
}

function ghReleaseExists(tagOrName) {
  return commandSucceeds("gh", ["release", "view", tagOrName]);
}

function goTagExists(tag) {
  try {
    const out = execFileSync("git", ["ls-remote", "--tags", "origin", `refs/tags/${tag}`], {
      encoding: "utf8",
      cwd: REPO_ROOT,
    });
    return out.trim().length > 0;
  } catch (err) {
    fail(`Failed to check for tag ${tag} on origin: ${err.message}`);
  }
}

function npmVersionPublished(npmConfig, name, version) {
  return commandSucceeds("npm", [
    "view",
    `${name}@${version}`,
    "version",
    `--registry=${npmConfig.registry}`,
  ]);
}

const npmConfigForReconcile = readJson(".github/publish-packages.config.json");
const managed = listManagedPackages();
const npmMatrix = [];
const goMatrix = [];

for (const pkg of managed) {
  const pkgJson = readJson(`${pkg.path}/package.json`);
  const version = pkgJson.version;

  if (pkg.isGoShim) {
    const info = goModuleInfo(pkg, version);
    if (!info) continue;
    const fullyPublished = goTagExists(info.tag) && ghReleaseExists(info.tag);
    if (!fullyPublished) goMatrix.push(info);
  } else {
    const releaseName = `${pkg.name}@${version}`;
    const fullyPublished =
      npmVersionPublished(npmConfigForReconcile, pkg.name, version) &&
      ghReleaseExists(releaseName);
    if (!fullyPublished) npmMatrix.push({ name: pkg.name, path: pkg.path, version });
  }
}

console.log(
  `Reconcile: ${npmMatrix.length} npm package(s), ${goMatrix.length} Go module(s) not fully published at their committed version.`,
);
writeOutput("npm-matrix", JSON.stringify(npmMatrix));
writeOutput("go-matrix", JSON.stringify(goMatrix));
writeOutput("has-npm-targets", String(npmMatrix.length > 0));
writeOutput("has-go-targets", String(goMatrix.length > 0));
