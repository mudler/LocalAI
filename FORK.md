# Fork operations

This fork (`rossigee/LocalAI`) tracks `mudler/LocalAI` master and adds our own
changes. This file is the operator's reference. The manifest that drives it is
`.github/fork/branches.yml`.

## Branch model

| Branch | Meaning |
| --- | --- |
| `upstream/master` | Upstream mudler/LocalAI master. Never committed to. |
| `fix/*`, `feat/*` | Our PR branches, each listed in `.github/fork/branches.yml`. |
| `develop-ci` | CI overlay: only the workflow and tooling commits that must not be upstream. It is kept on top of upstream/master. |
| `develop-next` | Candidate build of develop. Produced by the rebuild. Promoted only after CI is green. |
| `develop` | Integration branch and default branch. Its image is `ghcr.io/rossigee/localai:develop`. |

Invariant: `develop` = `upstream/master` + each PR branch in manifest order + the
`develop-ci` overlay.

A PR branch that has landed upstream is skipped automatically. Its `upstream_pr`
field in the manifest says where to check.

## Scripts

- `scripts/fork/status.sh [--json]`: read-only report of upstream, develop, the
  overlay and each PR branch (open or landed, drift).
- `scripts/fork/rebuild-develop.sh`: builds `develop-next` in a temporary worktree.
  Dry run by default. `--push` pushes `develop-next` only. It never touches
  `develop`. On a conflict it stops, prints the files, and pushes nothing.

## Procedures

### Rebuild and promote develop

1. `scripts/fork/rebuild-develop.sh`. Check the printed log.
2. `scripts/fork/rebuild-develop.sh --push`. CI builds `develop-next` (tag `next-<sha>`).
3. When the candidate image passes, promote: fast-forward `develop` to the
   candidate commit, tag it `develop/<date>-<upstream7>`, and record the upstream
   and PR-branch SHAs in the tag message.
4. The `develop` push builds `ghcr.io/rossigee/localai:develop` and `:develop-<sha>`.

### Add a PR branch

1. Add an entry to `.github/fork/branches.yml`, in the order it must merge.
2. Run `status.sh`, then a dry-run rebuild.

### A PR branch lands upstream

`status.sh` shows it as `landed`. Remove it from the manifest, then rebuild.
Nothing else changes.

### Upstream moves ahead

`status.sh` shows `develop` behind. Rebuild. A conflict means a PR branch needs a
rebase on upstream first (do that on its own branch, then rebuild).

### Conflicts

The rebuild exits 1 and names the files. Fix the PR branch, not `develop`. Never
resolve a conflict by editing `develop`.

## Rules

- Never force-push `develop`. Force-push `develop-next` and `develop-ci` only with
  `--force-with-lease`.
- `develop-ci` may be rewritten, but never deleted. Its protection allows force
  pushes and blocks deletion.
- Keep `backup/*` snapshots until a promoted tag has been verified.
- No AI attribution in commits or PRs.
- Every build passes the Go build with the tags `nosqlite,purego_utp`, plus
  `make protogen-go` and the React UI build first. A bare `go build ./...` fails.

## Tokens and secrets

- `FORK_PUSH_TOKEN` (fine-grained PAT: contents and workflows) pushes
  `develop-next` and `develop`, and dispatches workflows. The built-in token cannot
  change `.github/workflows/*`, and its pushes do not start other workflows.
- The image build uses `GITHUB_TOKEN` with `packages: write`.

## Known constraints

- Scheduled workflows run only from the default branch. The default is `develop`.
  Upstream's scheduled workflows also run there, so they are disabled in this fork
  (`gh workflow disable`).
- A new GHCR package is private until made public, and it must be linked to this
  repository.
