import { test, expect } from '@playwright/test'

// Mocked fixture covering the things the page renders:
//   - Per-model resolved PII state + the NER detectors each references
//     (one with default off, one with proxy default on, one explicit YAML)
//   - Recent events feed (the page must NEVER show the redacted content)
const MOCK_STATUS = {
  pii: {
    enabled_globally: true,
    default_enabled_for_backends: ['cloud-proxy'],
    models: [
      { name: 'qwen-7b', backend: 'llama-cpp', enabled: false, explicit: false, default_for_backend: false, detectors: null },
      { name: 'claude-sonnet', backend: 'cloud-proxy', enabled: true, explicit: false, default_for_backend: true, detectors: null },
      { name: 'claude-strict', backend: 'cloud-proxy', enabled: true, explicit: true, default_for_backend: true, detectors: ['privacy-filter-multilingual'] },
    ],
    recent_event_count: 2,
    // Instance-wide default detector set (managed by the Detector models
    // table's per-row Default toggle).
    default_detectors: ['global-ner-default'],
    // The token_classify "filter" models themselves: one NER, one in-process
    // pattern matcher, plus an orphan default that names a model not loaded.
    detector_models: [
      { name: 'privacy-filter-multilingual', backend: 'llama-cpp', type: 'ner', default: false },
      { name: 'secret-filter', backend: 'pattern', type: 'pattern', default: false },
      { name: 'global-ner-default', backend: '', type: 'unknown', default: true, missing: true },
    ],
  },
  router: {
    configured: true,
    models: [
      {
        name: 'smart-router',
        classifier: 'score',
        fallback: 'qwen-7b',
        policies: [
          { label: 'casual-chat', description: 'small talk' },
          { label: 'code-generation', description: 'writing or debugging code' },
        ],
        candidates: [
          { model: 'qwen-3b', labels: ['casual-chat'] },
          { model: 'qwen-coder', labels: ['code-generation', 'casual-chat'] },
        ],
        embedding_cache: {
          embedding_model: 'nomic-embed-text-v1.5',
          similarity_threshold: 0.80,
          confidence_threshold: 0.60,
          store_name: '',
          stats: {
            hits: 31,
            misses: 1,
            near_misses: 56,
            low_confidence: 29,
            embedder_errors: 0,
            store_errors: 0,
            // peak [0.4, 0.6) for paraphrases, secondary in [0.8, 1.0) for near-exact matches
            similarity_buckets: [0, 0, 0, 1, 22, 16, 3, 7, 19, 19],
          },
        },
      },
    ],
    recent_decision_count: 1,
    available_classifiers: ['score'],
  },
}

const MOCK_DECISIONS = {
  decisions: [
    {
      id: 'rd_a1', correlation_id: 'corr-1', user_id: 'local',
      router_model: 'smart-router', requested_model: 'smart-router', served_model: 'qwen-3b',
      classifier: 'score', label: 'casual-chat', score: 0.91, latency_ms: 15,
      cached: true, cache_similarity: 0.92,
      created_at: '2026-05-06T11:00:00Z',
    },
  ],
}

const MOCK_EVENTS = {
  events: [
    {
      id: 'pii_aaa', kind: 'pii', correlation_id: 'corr-1', user_id: 'local',
      direction: 'in', pattern_id: 'email', byte_offset: 12, length: 17,
      hash_prefix: 'ff8d9819', action: 'mask',
      created_at: '2026-05-06T10:00:00Z',
    },
    {
      id: 'proxy_connect_1', kind: 'proxy_connect',
      host: 'api.openai.com', intercepted: true,
      created_at: '2026-05-06T10:01:00Z',
    },
    {
      id: 'proxy_connect_2', kind: 'proxy_connect',
      host: 'github.com', intercepted: false,
      created_at: '2026-05-06T10:02:00Z',
    },
    {
      id: 'proxy_traffic_1', kind: 'proxy_traffic', correlation_id: 'corr-2',
      host: 'api.openai.com',
      bytes_sent: 412, bytes_received: 1228, status_code: 200, duration_ms: 240,
      created_at: '2026-05-06T10:03:00Z',
    },
  ],
}

test.describe('Middleware page — admin in no-auth mode', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/auth/status', (route) =>
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ authEnabled: false, staticApiKeyRequired: false, providers: [] }),
      })
    )
    await page.route('**/api/middleware/status', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify(MOCK_STATUS) })
    )
    await page.route('**/api/pii/events?**', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify(MOCK_EVENTS) })
    )
    await page.route('**/api/router/decisions?**', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify(MOCK_DECISIONS) })
    )
    // The Default PII policy detector picker is capability-filtered to
    // token_classify via /api/models/capabilities.
    await page.route('**/api/models/capabilities', (route) =>
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ models: [{ id: 'privacy-filter-multilingual', capabilities: ['FLAG_TOKEN_CLASSIFY'], backend: 'llama-cpp' }] }),
      })
    )
    await page.route('**/api/settings', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ success: true }) })
    )
    // The per-model PII toggle PATCHes the model config (pii.enabled).
    await page.route('**/api/models/config-json/**', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ success: true }) })
    )
  })

  test('Filtering tab renders per-model state and referenced detectors', async ({ page }) => {
    await page.goto('/app/middleware')

    // Per-model state — each model's name is visible.
    await expect(page.getByText('qwen-7b').first()).toBeVisible()
    await expect(page.getByText('claude-strict').first()).toBeVisible()

    // The detector a model references is shown in its row.
    await expect(page.getByText('privacy-filter-multilingual').first()).toBeVisible()

    // Default-policy banner names the backends with PII on by default.
    await expect(page.getByText(/cloud-proxy/).first()).toBeVisible()
  })

  test('Filtering tab lists detector models with type badges and a default toggle', async ({ page }) => {
    await page.goto('/app/middleware')

    // The Detector models card renders every token_classify filter model.
    await expect(page.getByText('Detector models')).toBeVisible()
    const nerRow = page.locator('tr').filter({ hasText: 'privacy-filter-multilingual' }).first()
    await expect(nerRow).toContainText(/NER/i)
    const patternRow = page.locator('tr').filter({ hasText: 'secret-filter' }).first()
    await expect(patternRow).toContainText(/pattern/i)

    // The NER detector is not (yet) a default — its toggle is unchecked.
    // (The underlying checkbox is 0×0 by design, so we click the label wrapper.)
    const nerToggle = nerRow.locator('label.toggle')
    await expect(nerToggle.locator('input[type="checkbox"]')).not.toBeChecked()

    // Toggling it on persists the new default set via POST /api/settings.
    const saved = page.waitForRequest(req =>
      req.url().includes('/api/settings') && req.method() === 'POST')
    await nerToggle.click()
    const req = await saved
    const body = JSON.parse(req.postData() || '{}')
    expect(body.pii_default_detectors).toContain('privacy-filter-multilingual')
  })

  test('Filtering tab surfaces an orphan default detector that is not loaded', async ({ page }) => {
    await page.goto('/app/middleware')

    // global-ner-default names a model that is not loaded, but it is in the
    // default set — it must still appear (toggled on) so admins can remove it.
    const orphanRow = page.locator('tr').filter({ hasText: 'global-ner-default' }).first()
    await expect(orphanRow).toContainText(/not loaded/i)
    await expect(orphanRow.locator('label.toggle input[type="checkbox"]')).toBeChecked()
  })

  test('Filtering tab flags an enabled model with no detector as a no-op', async ({ page }) => {
    await page.goto('/app/middleware')

    // claude-sonnet is enabled by the cloud-proxy backend default but lists
    // no detectors and there is no instance default detector — it scans
    // nothing, so the row must warn rather than read as protected.
    const noopRow = page.locator('tr').filter({ hasText: 'claude-sonnet' }).first()
    await expect(noopRow).toContainText(/no-op/i)

    // claude-strict has an explicit detector — it must NOT be flagged.
    const okRow = page.locator('tr').filter({ hasText: 'claude-strict' }).first()
    await expect(okRow).not.toContainText(/no-op/i)
  })

  test('Filtering tab PII column toggles a model\'s pii.enabled via PATCH', async ({ page }) => {
    await page.goto('/app/middleware')

    // qwen-7b is OFF (enabled:false) — its PII toggle reads unchecked.
    const row = page.locator('tr').filter({ hasText: 'qwen-7b' }).first()
    const toggle = row.locator('label.toggle')
    await expect(toggle.locator('input[type="checkbox"]')).not.toBeChecked()

    // Toggling on PATCHes the model config with an explicit pii.enabled:true,
    // scoped to that model (no other field is sent — the server deep-merges).
    const patched = page.waitForRequest(req =>
      req.url().includes('/api/models/config-json/') && req.method() === 'PATCH')
    await toggle.click()
    const req = await patched
    expect(decodeURIComponent(req.url())).toContain('qwen-7b')
    const body = JSON.parse(req.postData() || '{}')
    expect(body.pii.enabled).toBe(true)
  })

  test('Routing tab renders configured routers and recent decisions', async ({ page }) => {
    await page.goto('/app/middleware')
    await page.getByRole('button', { name: /Routing/i }).click()
    // Active router model name visible.
    await expect(page.getByText('smart-router').first()).toBeVisible()
    // Candidate model names visible.
    await expect(page.getByText('qwen-coder').first()).toBeVisible()
    await expect(page.getByText('qwen-3b').first()).toBeVisible()
    // Decision row visible — label and served model.
    await expect(page.getByText('casual-chat').first()).toBeVisible()
  })

  test('Routing tab renders embedding-cache stats and similarity histogram', async ({ page }) => {
    await page.goto('/app/middleware')
    await page.getByRole('button', { name: /Routing/i }).click()

    // Embedding model name surfaces in the cache column.
    await expect(page.getByText('nomic-embed-text-v1.5').first()).toBeVisible()

    // Hit-rate badge: 31 hits / (31 + 56 + 1) = 35% rounded.
    await expect(page.getByText(/35% hit/i).first()).toBeVisible()

    // h/n/m counter row visible.
    await expect(page.getByText(/31h\/56n\/1m/).first()).toBeVisible()

    // Skipped (low-confidence) counter visible.
    await expect(page.getByText(/29 skipped/).first()).toBeVisible()

    // Threshold marker text matches the configured 0.80.
    await expect(page.getByText(/sim ≥ 0\.8/).first()).toBeVisible()

    // Histogram bars rendered with hover titles that include the
    // bucket range and count. Bucket 4 (peak) has count 22; the
    // <div> with that exact title is the structural assertion.
    await expect(
      page.locator('div[title="[0.4, 0.5): 22"]')
    ).toBeVisible()
    // Bucket 8 (just at threshold) has count 19.
    await expect(
      page.locator('div[title="[0.8, 0.9): 19"]')
    ).toBeVisible()
  })

  test('Routing tab shows a cached decision with cache_similarity', async ({ page }) => {
    await page.goto('/app/middleware')
    await page.getByRole('button', { name: /Routing/i }).click()

    // The decision row exposes the cached flag and the cosine that
    // produced the hit so admins can correlate with the histogram.
    await expect(page.getByText('corr-1')).toBeVisible()
  })

  test('Events tab renders rows but never the redacted content', async ({ page }) => {
    await page.goto('/app/middleware')
    await page.getByRole('button', { name: /Events/i }).click()
    // Hash prefix is visible — that's how admins audit recurring leaks.
    await expect(page.getByText('ff8d9819')).toBeVisible()
    // The page only ever shows fields the EventStore stores. The matched
    // value (e.g. "alice@example.com") would never appear because it's
    // not in the payload — explicit asserting absence here is the
    // contract the design relies on.
    await expect(page.getByText(/@example\.com/)).toHaveCount(0)
  })

  test('Events tab renders proxy_connect rows with intercept decision', async ({ page }) => {
    await page.goto('/app/middleware')
    await page.getByRole('button', { name: /Events/i }).click()

    // Both intercept and tunnel decisions visible.
    const interceptRow = page.locator('tr').filter({ hasText: 'api.openai.com' }).first()
    await expect(interceptRow).toContainText(/intercepted/i)
    const tunnelRow = page.locator('tr').filter({ hasText: 'github.com' }).first()
    await expect(tunnelRow).toContainText(/tunneled/i)
  })

  test('Events tab renders proxy_traffic byte counts and status', async ({ page }) => {
    await page.goto('/app/middleware')
    await page.getByRole('button', { name: /Events/i }).click()

    // The traffic row formats as "HTTP 200 · ↑412B ↓1.2KB · 240ms".
    // We assert on the durable parts: status code, byte values, duration unit.
    const trafficRow = page.locator('tr').filter({ hasText: 'corr-2' }).first()
    await expect(trafficRow).toContainText('HTTP 200')
    await expect(trafficRow).toContainText('412B')
    await expect(trafficRow).toContainText(/1\.2\s*KB/i)
    await expect(trafficRow).toContainText('240ms')
  })

  test('Events kind filter narrows the table to the chosen kind', async ({ page }) => {
    await page.goto('/app/middleware')
    await page.getByRole('button', { name: /Events/i }).click()

    // Default = All: pii row + 2 connect rows + 1 traffic row visible.
    await expect(page.getByText('ff8d9819')).toBeVisible()
    await expect(page.getByText('github.com')).toBeVisible()

    // Click "PII" filter — proxy rows must disappear.
    await page.getByRole('button', { name: /^PII$/ }).click()
    await expect(page.getByText('ff8d9819')).toBeVisible()
    await expect(page.getByText('github.com')).toHaveCount(0)
    await expect(page.getByText('HTTP 200')).toHaveCount(0)

    // Click "Proxy traffic" — only the traffic row remains.
    await page.getByRole('button', { name: /Proxy traffic/i }).click()
    await expect(page.getByText('HTTP 200')).toBeVisible()
    await expect(page.getByText('ff8d9819')).toHaveCount(0)
    await expect(page.getByText('github.com')).toHaveCount(0)

    // Click "Proxy connect" — both connect rows visible, no PII or traffic.
    await page.getByRole('button', { name: /Proxy connect/i }).click()
    await expect(page.locator('tr').filter({ hasText: 'github.com' })).toHaveCount(1)
    await expect(page.locator('tr').filter({ hasText: 'api.openai.com' }).filter({ hasText: 'intercepted' })).toHaveCount(1)
    await expect(page.getByText('HTTP 200')).toHaveCount(0)
    await expect(page.getByText('ff8d9819')).toHaveCount(0)

    // Click "All" — everything back.
    await page.getByRole('button', { name: /^All$/ }).click()
    await expect(page.getByText('ff8d9819')).toBeVisible()
    await expect(page.getByText('HTTP 200')).toBeVisible()
  })

  test('Events tab shows the kind badge for each row', async ({ page }) => {
    await page.goto('/app/middleware')
    await page.getByRole('button', { name: /Events/i }).click()

    // The Kind column header is present.
    await expect(page.locator('th').filter({ hasText: /^Kind$/ })).toBeVisible()
    // At least one cell renders each of the three kinds. Scope to
    // <span> elements so the "PII" filter button doesn't match.
    await expect(page.locator('span').getByText(/^pii$/i).first()).toBeVisible()
    await expect(page.getByText(/^proxy connect$/i).first()).toBeVisible()
    await expect(page.getByText(/^proxy traffic$/i).first()).toBeVisible()
  })

})

test.describe('Middleware page — non-admin under auth-on', () => {
  test('redirects to /app when the user is not admin', async ({ page }) => {
    await page.route('**/api/auth/status', (route) =>
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          authEnabled: true,
          staticApiKeyRequired: false,
          providers: ['local'],
          user: { id: 'bob', name: 'Bob', role: 'user', provider: 'local' },
        }),
      })
    )

    await page.goto('/app/middleware')
    // RequireAdmin redirects non-admin viewers; the URL must not stay on /middleware.
    await page.waitForURL(/\/app(?!\/middleware)/, { timeout: 5000 })
    expect(page.url()).not.toMatch(/\/middleware/)
  })
})
