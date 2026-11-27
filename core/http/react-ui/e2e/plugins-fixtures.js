// The plugin API stubbed for the Plugins tab specs (plugins.spec.js): two
// marketplaces, a catalog with a text-only plugin, one that runs code, one
// not checked yet and one LocalAI cannot fetch, the review of each, the
// installs, the per-user switch (with needs_reenable) and an update review.

const AT = '2026-11-26T10:00:00Z'
export const SHA = '0123456789abcdef0123456789abcdef01234567'
const DIGEST = 'sha256:' + 'ab'.repeat(32)

export const MARKETS = [
  { id: 'localai', name: 'LocalAI', kind: 'github', location: 'mudler/LocalAI', enabled: true, default: true, suspended: false, active: true, headSha: SHA, refreshedAt: AT, createdAt: AT, updatedAt: AT },
  { id: 'acme', name: 'Acme tools', kind: 'git', location: 'https://git.example.com/acme/plugins.git', enabled: true, default: false, suspended: false, active: true, headSha: SHA, refreshedAt: AT, lastError: '', createdAt: AT, updatedAt: AT },
]
export const ITEMS = [
  { name: 'docs-helper', description: 'Write and review documentation.', version: '1.2.0', category: 'writing', supported: true, plugin: 'docs-helper@localai', marketplace: 'localai', runsCode: false, installs: [] },
  { name: 'lint-hooks', description: 'Runs a linter after each edit.', version: '0.3.0', category: 'code', author: 'Acme', supported: true, plugin: 'lint-hooks@acme', marketplace: 'acme', runsCode: true, installs: [] },
  { name: 'notes-kit', description: 'Nobody has reviewed or installed it yet.', version: '2.0.0', supported: true, plugin: 'notes-kit@acme', marketplace: 'acme', installs: [] },
  { name: 'npm-thing', description: 'From npm.', supported: false, reason: 'npm sources are not supported', plugin: 'npm-thing@acme', marketplace: 'acme', installs: [] },
]
const MODEL_TEXT = {
  name: 'docs-helper',
  skills: [{ name: 'style', description: 'House style', userInvocable: true }, { name: 'review', description: 'Review a page', userInvocable: true, argumentHint: '<file>' }],
  commands: [{ name: 'outline', description: 'Outline a page', argumentHint: '<topic>' }],
  mcpServers: [{ name: 'search', type: 'http', url: 'https://docs.example.com/mcp' }],
}
const MODEL_CODE = {
  name: 'lint-hooks',
  skills: [],
  hooks: [{ event: 'PostToolUse', matcher: 'Edit', handlers: [{ type: 'command', command: '${CLAUDE_PLUGIN_ROOT}/hooks/lint.sh' }] }],
}

function preview(entry, model, runsCode) {
  return {
    token: `tok-${entry.name}`, expiresAt: AT, plugin: entry.plugin, name: entry.name, version: entry.version, marketplace: entry.marketplace,
    entry: entry.name, sha: SHA, digest: DIGEST, sizeBytes: 4096, fileCount: 5, files: [], model, runsCode,
    codeReasons: runsCode ? ['hook-command'] : [], codeFiles: runsCode ? ['hooks/hooks.json', 'hooks/lint.sh'] : [],
    hosts: runsCode ? [] : ['docs.example.com'],
  }
}

export async function stubPlugins(page, { installs = [] } = {}) {
  const api = { installs: [...installs], posts: [], mePuts: [], updates: [], added: [] }
  // The Plugins tab lives on the Add-ons page, which a server with the add-on
  // runtime off does not serve: this test server is one, so the page reads
  // the runtime as on.
  await page.route('**/api/features', async (route) => {
    const res = await route.fetch()
    const json = await res.json()
    return route.fulfill({ response: res, json: { ...json, addons: true } })
  })
  await page.route('**/api/plugins/marketplaces', (route) => {
    if (route.request().method() === 'POST') {
      api.added.push(route.request().postDataJSON())
      return route.fulfill({ json: { ...MARKETS[1], id: 'team', name: 'team' } })
    }
    return route.fulfill({ json: { marketplaces: MARKETS } })
  })
  await page.route(/\/api\/plugins\/catalog\/[^/?]+\/[^/?]+$/, (route) => {
    const [mkt, name] = route.request().url().split('/').slice(-2)
    const item = ITEMS.find((i) => i.plugin === `${name}@${mkt}`)
    return item ? route.fulfill({ json: item }) : route.fulfill({ status: 404, json: { error: { type: 'not_found', message: 'no such entry' } } })
  })
  await page.route(/\/api\/plugins\/catalog(\?.*)?$/, (route) => {
    const q = new URL(route.request().url()).searchParams.get('q') || ''
    const items = ITEMS.filter((i) => !q || i.name.includes(q))
    return route.fulfill({ json: { items, total: items.length, offset: 0, limit: 48 } })
  })
  await page.route('**/api/plugins/inspect', (route) => {
    const { plugin } = route.request().postDataJSON()
    const entry = ITEMS.find((i) => i.plugin === plugin)
    const code = plugin.startsWith('lint') || plugin.startsWith('notes')
    return route.fulfill({ json: code ? preview(entry, { ...MODEL_CODE, name: entry.name }, true) : preview(entry, MODEL_TEXT, false) })
  })
  await page.route(/\/api\/plugins\/inspect\/[^/]+\/files\/.+/, (route) => route.fulfill({
    status: 200, contentType: 'text/plain; charset=utf-8', body: '#!/bin/sh\necho "<b>not html</b>"\n',
  }))
  await page.route('**/api/plugins/installs', (route) => {
    if (route.request().method() === 'POST') {
      api.posts.push({ body: route.request().postDataJSON(), confirm: route.request().headers()['x-localai-confirm'] || '' })
      return route.fulfill({ status: 202, json: { jobID: 'job-1', plugin: 'x' } })
    }
    return route.fulfill({ json: { installs: api.installs } })
  })
  await page.route(/\/api\/plugins\/installs\/[^/]+\/me$/, (route) => {
    const body = route.request().postDataJSON()
    api.mePuts.push(body)
    const id = route.request().url().split('/').at(-2)
    const inst = api.installs.find((i) => i.id === id)
    if (inst.needsReenable && body.enabled && !body.reenable) {
      return route.fulfill({ status: 409, json: { error: { type: 'needs_reenable', message: 'This plugin now runs code. Turn it on again to accept it.' } } })
    }
    inst.enabled = Boolean(body.enabled)
    inst.needsReenable = false
    return route.fulfill({ json: { install: id, enabled: inst.enabled, needsReenable: false, secretsSet: [] } })
  })
  await page.route(/\/api\/plugins\/installs\/[^/]+\/update\/preview$/, (route) => route.fulfill({ json: {
    token: 'up-1', expiresAt: AT, install: 'in-code', plugin: 'lint-hooks@acme', fromSha: SHA, toSha: 'fedcba9876543210fedcba9876543210fedcba98', fromDigest: DIGEST, toDigest: DIGEST,
    added: [], removed: [], changed: [{ path: 'hooks/lint.sh', oldLines: 3, newLines: 4, code: true }, { path: 'README.md', oldLines: 10, newLines: 12, code: false }],
    diffs: [{ path: 'hooks/lint.sh', diff: '--- a/hooks/lint.sh\n+++ b/hooks/lint.sh\n@@ -1,3 +1,4 @@\n #!/bin/sh\n-eslint .\n+eslint --fix .\n+echo done\n' }],
    truncated: false, oldCodeReasons: ['hook-command'], newCodeReasons: ['hook-command'], needsCodeReview: true, codeFiles: ['hooks/lint.sh'],
  } }))
  await page.route(/\/api\/plugins\/installs\/[^/]+\/update$/, (route) => {
    api.updates.push({ body: route.request().postDataJSON(), confirm: route.request().headers()['x-localai-confirm'] || '' })
    return route.fulfill({ status: 202, json: { jobID: 'job-2', plugin: 'lint-hooks@acme' } })
  })
  await page.route(/\/api\/plugins\/installs\/[^/]+$/, (route) => {
    const id = route.request().url().split('/').at(-1)
    const inst = api.installs.find((i) => i.id === id)
    return route.fulfill({ json: { ...inst, model: inst.runsCode ? MODEL_CODE : MODEL_TEXT, files: [], sizeBytes: 2048, codeReasons: inst.runsCode ? ['hook-command'] : [], codeFiles: inst.runsCode ? ['hooks/lint.sh'] : [], skipped: [] } })
  })
  return api
}

export const INSTALLED = [
  { id: 'in-text', plugin: 'docs-helper@localai', name: 'docs-helper', version: '1.2.0', scope: 'instance', digest: DIGEST, sha: SHA, runsCode: false, state: 'installed', enabled: true, needsReenable: false, createdAt: AT, updatedAt: AT, approvedAt: AT },
  { id: 'in-code', plugin: 'lint-hooks@acme', name: 'lint-hooks', version: '0.3.0', scope: 'instance', digest: DIGEST, sha: SHA, runsCode: true, state: 'update_available', updateSha: 'fedcba98', enabled: false, needsReenable: true, createdAt: AT, updatedAt: AT, approvedAt: AT },
]

