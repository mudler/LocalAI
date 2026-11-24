# CI Bot Pull Requests

Scheduled workflows open pull requests with `peter-evans/create-pull-request`: dependency bumps, docs version, swagger, checksums, model gallery additions, variant proposals, site counters and inference defaults. They push a branch to this repository and open the PR from it.

## Why a GitHub App token

A pull request opened with the default `GITHUB_TOKEN` does not trigger `pull_request` workflows, so CI would never run on the bot PRs. These workflows mint a short-lived installation token of a GitHub App with `actions/create-github-app-token` and pass it to `create-pull-request`. No personal access token and no fork are involved.

## Setup

1. Create a GitHub App with these repository permissions:
   - Contents: read and write
   - Pull requests: read and write
   - Metadata: read (always granted)
2. Install the App on this repository.
3. Set the App ID in the repository variable `UPDATE_BOT_APP_ID`.
4. Store the App private key (PEM) in the repository secret `UPDATE_BOT_PRIVATE_KEY`.

Each workflow requests only the permissions it needs through the `permission-*` inputs of the token action. If a bot job ever edits files under `.github/workflows/`, also grant the App Workflows: read and write and request `permission-workflows: write` in that job.

## Conventions

- The token step sits right before the step that uses it, and carries the same `if:` condition, so no token is minted when there is nothing to push.
- Jobs keep `permissions: contents: read` for `GITHUB_TOKEN`. Writes go through the App token only.
- Bot branches (`bump/*`, `update/*`, `propose/*`, `chore/*`, `create-pull-request/*`) live in this repository. Do not create human branches with these prefixes: `create-pull-request` force-pushes them.

## Cleanup after the cutover

Once the App token works, remove the repository secret `UPDATE_BOT_TOKEN` and the bot fork, and delete the stale fork branches.
