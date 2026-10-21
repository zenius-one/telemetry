#!/usr/bin/env node
// Keeps .changeset/config.json's "ignore" list in sync with the two
// existing eligibility configs, so there's exactly one place to edit
// scope (publish-packages.config.json for npm, publish-go-packages.config.json
// for Go) rather than a third copy of the same include/exclude logic
// living inside Changesets' own config.
//
// Changesets' "ignore" takes package NAMES, not paths, and means "don't
// offer this package in `pnpm changeset`, don't version it, don't publish
// it." Everything in the pnpm workspace that ISN'T eligible per the two
// configs below gets ignored — tools/*, and any packages/* package not
// explicitly opted in.
//
// apps/* are the one deliberate exception: they're never eligible for
// publish-packages.config.json / publish-go-packages.config.json (those
// only ever list packages/* — apps are deployable, not libraries, and are
// never tagged), but they're still deliberately kept OUT of the ignore
// list so Changesets versions them and writes their CHANGELOG.md
// (.changeset/config.json's privatePackages.version handles the actual
// bump; privatePackages.tag: false stops them getting git tags). What
// stops an app from being published is its own package.json "private":
// true, checked below, plus the fact that resolve-changeset-release.mjs's
// npm matrix is built from publish-packages.config.json's "include",
// which never lists apps/* — not the changeset ignore list. There is no
// `changeset publish` step in this repo (see publish-packages.yml);
// publishing is a separate CI/CD concern from versioning. This applies
// equally to a Go app (a package.json added purely so Changesets can
// version/changelog it, sitting next to its go.mod) — see below for how
// that's told apart from a real Go module shim.
//
// A Go module's package.json (see .github/README.md) is a
// version-tracking shim, not a real npm package — identified by having a
// go.mod next to it, never by convention alone. Shims are only ever
// recognized under packages/*, and are how a Go module gets tagged +
// released. A go.mod + package.json pair under apps/* is NOT a shim — an
// app's own package.json is Changesets tracking, same as any JS app,
// never tag-worthy — so apps/* is exempt from the packages/*-only rule
// below; only a genuinely misplaced shim (go.mod + package.json outside
// both packages/* and apps/*) hard-fails.
//
// Usage:
//   node sync-changeset-ignore.mjs         # check mode: exit 1 if stale
//   node sync-changeset-ignore.mjs --write # write mode: regenerate in place

import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";

const REPO_ROOT = process.cwd();
const WRITE = process.argv.includes("--write");

function fail(message) {
  console.error(`::error::${message}`);
  process.exit(1);
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

const npmConfig = readJson(".github/publish-packages.config.json");
const goConfig = readJson(".github/publish-go-packages.config.json");
const changesetConfigPath = path.join(REPO_ROOT, ".changeset/config.json");
const changesetConfig = readJson(".changeset/config.json");

let workspacePackages;
try {
  const raw = execFileSync(
    "pnpm",
    ["-r", "list", "--json", "--depth", "-1"],
    { encoding: "utf8", cwd: REPO_ROOT },
  );
  workspacePackages = JSON.parse(raw);
} catch (err) {
  fail(`Failed to list pnpm workspace packages: ${err.message}`);
}

const ignore = [];

for (const pkg of workspacePackages) {
  if (!pkg.name || pkg.private === undefined) continue; // root project entry
  const relPath = path.relative(REPO_ROOT, pkg.path).split(path.sep).join("/");
  const isApp = relPath.startsWith("apps/");
  const isGoShim = existsSync(path.join(REPO_ROOT, relPath, "go.mod"));

  if (isApp) {
    // Always eligible (never ignored) — see the file-header comment. The
    // one thing enforced here rather than left to publish-packages.config.json
    // / publish-go-packages.config.json is that an app can never
    // accidentally end up publishable: it must be "private": true in its
    // own package.json. This applies whether or not the app also has a
    // go.mod next to it (e.g. apps/example) — an app's package.json is
    // Changesets tracking, never a tag-worthy Go module shim, so it's
    // exempt from the packages/*-only shim rule below.
    if (pkg.private !== true) {
      fail(
        `${relPath} is an app (apps/*) but isn't "private": true in its ` +
          `package.json. Apps are versioned and changelogged by ` +
          `Changesets but must never be published — mark it private.`,
      );
    }
  } else if (isGoShim) {
    if (!relPath.startsWith("packages/")) {
      fail(
        `${relPath} has a go.mod AND a package.json outside packages/ — ` +
          `Go module shims are only ever allowed under packages/*, never ` +
          `apps/* (apps are deployable, not tagged). Remove this shim.`,
      );
    }
    const eligible =
      matchesAny(relPath, goConfig.include ?? []) &&
      !matchesAny(relPath, goConfig.exclude ?? []);
    if (!eligible) ignore.push(pkg.name);
  } else {
    const eligible =
      matchesAny(relPath, npmConfig.include ?? []) &&
      !matchesAny(relPath, npmConfig.exclude ?? []) &&
      pkg.private !== true;
    if (!eligible) ignore.push(pkg.name);
  }
}

ignore.sort();

const isStale = JSON.stringify(changesetConfig.ignore ?? []) !== JSON.stringify(ignore);

if (WRITE) {
  if (isStale) {
    changesetConfig.ignore = ignore;
    writeFileSync(changesetConfigPath, `${JSON.stringify(changesetConfig, null, 2)}\n`);
    console.log(`Wrote ${ignore.length} ignored package(s) to .changeset/config.json.`);
  } else {
    console.log(".changeset/config.json ignore list already up to date.");
  }
} else if (isStale) {
  fail(
    ".changeset/config.json's \"ignore\" list is stale. Run " +
      "`node .github/scripts/sync-changeset-ignore.mjs --write` and commit the result.\n" +
      `Expected: ${JSON.stringify(ignore)}\n` +
      `Found:    ${JSON.stringify(changesetConfig.ignore ?? [])}`,
  );
} else {
  console.log(".changeset/config.json ignore list is up to date.");
}
