import fs from "fs";
import yaml from "js-yaml";
import { Octokit } from "@octokit/core";

// Load backend.yml and parse matrix.include
const backendYml = yaml.load(fs.readFileSync(".github/workflows/backend.yml", "utf8"));
const jobs = backendYml.jobs;
const backendJobs = jobs["backend-jobs"];
const backendJobsDarwin = jobs["backend-jobs-darwin"];
const includes = backendJobs.strategy.matrix.include;
const includesDarwin = backendJobsDarwin.strategy.matrix.include;

// Set up Octokit for PR changed files
const token = process.env.GITHUB_TOKEN;
const octokit = new Octokit({ auth: token });

const eventPath = process.env.GITHUB_EVENT_PATH;
const event = JSON.parse(fs.readFileSync(eventPath, "utf8"));

let prNumber, repo, owner;
if (event.pull_request) {
  prNumber = event.pull_request.number;
  repo = event.repository.name;
  owner = event.repository.owner.login;
} else {
  throw new Error("This workflow must be triggered by a pull_request event.");
}

async function getChangedFiles() {
  let files = [];
  let page = 1;
  while (true) {
    const res = await octokit.request('GET /repos/{owner}/{repo}/pulls/{pull_number}/files', {
      owner,
      repo,
      pull_number: prNumber,
      per_page: 100,
      page
    });
    files = files.concat(res.data.map(f => f.filename));
    if (res.data.length < 100) break;
    page++;
  }
  return files;
}

// Infer backend path
function inferBackendPath(item) {
  if (item.dockerfile.endsWith("python")) {
    return `backend/python/${item.backend}/`;
  }
  if (item.dockerfile.endsWith("golang")) {
    return `backend/go/${item.backend}/`;
  }
  if (item.dockerfile.endsWith("llama-cpp")) {
    return `backend/cpp/llama-cpp/`;
  }
  return null;
}

function inferBackendPathDarwin(item) {
  if (!item.lang) {
    return `backend/python/${item.backend}/`;
  }

  return `backend/${item.lang}/${item.backend}/`;
}

(async () => {
  const changedFiles = await getChangedFiles();

  console.log("Changed files:", changedFiles);

  const filtered = includes.filter(item => {
    const backendPath = inferBackendPath(item);
    if (!backendPath) return false;
    return changedFiles.some(file => file.startsWith(backendPath));
  });

  const filteredDarwin = includesDarwin.filter(item => {
    const backendPath = inferBackendPathDarwin(item);
    return changedFiles.some(file => file.startsWith(backendPath));
  })

  console.log("Filtered files:", filtered);
  console.log("Filtered files Darwin:", filteredDarwin);

  const hasBackends = filtered.length > 0 ? 'true' : 'false';
  const hasBackendsDarwin = filteredDarwin.length > 0 ? 'true' : 'false';
  console.log("Has backends?:", hasBackends);
  console.log("Has Darwin backends?:", hasBackendsDarwin);

  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-backends=${hasBackends}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `has-backends-darwin=${hasBackendsDarwin}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `matrix=${JSON.stringify({ include: filtered })}\n`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `matrix-darwin=${JSON.stringify({ include: filteredDarwin })}\n`);
})();
