#!/usr/bin/env node
//
// CI entrypoint for the mutable-tag ordering guard. Reads the tag list
// docker/metadata-action produced, decides which tags this build is allowed to
// publish (see scripts/lib/tag-guard.mjs for the rules and the incident that
// motivated them), prints the surviving tags one per line on stdout, and logs
// every decision to stderr, the GitHub job summary and, for anything blocked or
// unverifiable, a workflow annotation.
//
// The whole point of this guard is to be noisy: the bug it fixes ran silently
// for two days. Never make a skipped tag a quiet no-op.
//
// Inputs (environment):
//   DOCKER_METADATA_OUTPUT_JSON  metadata-action output; .tags is used
//   TAGS                         newline/comma separated tags (alternative)
//   GITHUB_SHA                   commit being published
//   GITHUB_REPOSITORY            owner/name, for the compare API
//   GITHUB_TOKEN                 optional, raises the compare API rate limit
//   REGISTRY_PREFIX              optional, keep only tags with this prefix
//
// Exit codes:
//   0  decisions made (blocked tags are not an error; the build still ships
//      its immutable sha- tag, which is the artifact humans pin)
//   1  bad input, e.g. no tag list at all

import fs from "node:fs";

import { guardTags } from "./lib/tag-guard.mjs";

function readTags() {
  const raw = process.env.DOCKER_METADATA_OUTPUT_JSON;
  if (raw) {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed.tags)) {
      return parsed.tags;
    }
  }
  if (process.env.TAGS) {
    return process.env.TAGS.split(/[\n,]/)
      .map((t) => t.trim())
      .filter(Boolean);
  }
  return [];
}

function appendSummary(lines) {
  const path = process.env.GITHUB_STEP_SUMMARY;
  if (!path) {
    return;
  }
  try {
    fs.appendFileSync(path, `${lines.join("\n")}\n`);
  } catch (err) {
    process.stderr.write(`tag-guard: could not write job summary: ${err}\n`);
  }
}

async function main() {
  const prefix = process.env.REGISTRY_PREFIX || "";
  const refs = readTags().filter((t) => t.startsWith(prefix));
  if (refs.length === 0) {
    process.stderr.write(
      `tag-guard: no tags to consider (REGISTRY_PREFIX=${prefix || "<none>"})\n`,
    );
    return;
  }

  const incomingSha = process.env.GITHUB_SHA;
  if (!incomingSha) {
    throw new Error("GITHUB_SHA is not set");
  }

  const { allowed, blocked, decisions } = await guardTags({
    refs,
    incomingSha,
    repository: process.env.GITHUB_REPOSITORY || "mudler/LocalAI",
    token: process.env.GITHUB_TOKEN,
  });

  const summary = ["", `#### Tag ordering guard (${incomingSha.slice(0, 7)})`, ""];
  for (const d of decisions) {
    process.stderr.write(`tag-guard: ${d.push ? "PUSH " : "SKIP "} ${d.reason}\n`);
    summary.push(`- ${d.push ? "published" : "**skipped**"}: ${d.reason}`);
    // Annotate anything that is not a plain, verified advance so it shows up on
    // the run page without anyone having to open the log.
    if (!d.push || d.severity === "warning") {
      process.stderr.write(`::warning title=backend tag guard::${d.reason}\n`);
    }
  }
  appendSummary(summary);

  if (blocked.length > 0) {
    process.stderr.write(
      `tag-guard: ${blocked.length} mutable tag(s) withheld; ` +
        `${allowed.length} tag(s) will be published\n`,
    );
  }

  process.stdout.write(`${allowed.join("\n")}\n`);
}

main().catch((err) => {
  process.stderr.write(`tag-guard: ${err && err.stack ? err.stack : err}\n`);
  process.exit(1);
});
