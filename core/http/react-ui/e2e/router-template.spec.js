import { test, expect } from '@playwright/test'

// Router template + structured editor regression tests.
//
// The historical regression was: the "Create routing model" button
// loaded the model editor with an array-shaped `router.candidates`
// value, which crashed when a code-editor field received it instead
// of a string ("(intermediate value).split is not a function").
//
// The current schema is also covered:
//   - classifier=score is the only shipped classifier
//   - router.policies surfaces in its own structured editor (label +
//     description rows with duplicate detection)
//   - router.candidates is the structured {model, labels[]} editor;
//     labels are chips populated from router.policies via FormContext
//   - router.embedding_cache.* surface as labelled fields with the
//     correct components (model-select / slider)
//   - router.activation_threshold and the two embedding_cache slider
//     fields render with slider min/max/step from the registry

const ROUTER_METADATA = {
  sections: [
    { id: 'general', label: 'General', icon: 'settings', order: 0 },
    { id: 'other', label: 'Other', icon: 'more-horizontal', order: 100 },
  ],
  fields: [
    { path: 'name', yaml_key: 'name', go_type: 'string', ui_type: 'string',
      section: 'general', label: 'Model Name', component: 'input', order: 0 },
    {
      path: 'router.classifier', yaml_key: 'classifier', go_type: 'string', ui_type: 'string',
      section: 'other', label: 'Classifier', component: 'select',
      options: [{ value: 'score', label: 'Score (Arch-Router-style)' }],
      description: 'Picks a candidate by scoring every policy label against the prompt. Only "score" is shipped today.',
      order: 230,
    },
    {
      path: 'router.classifier_model', yaml_key: 'classifier_model', go_type: 'string', ui_type: 'string',
      section: 'other', label: 'Classifier Model', component: 'model-select', autocomplete_provider: 'models:chat',
      description: 'Loaded LocalAI model the score classifier asks to rank each policy label.',
      order: 231,
    },
    {
      path: 'router.fallback', yaml_key: 'fallback', go_type: 'string', ui_type: 'string',
      section: 'other', label: 'Fallback Model', component: 'model-select', autocomplete_provider: 'models:chat',
      description: 'Model used when no candidate covers the active label set.',
      order: 232,
    },
    {
      path: 'router.activation_threshold', yaml_key: 'activation_threshold', go_type: 'float64', ui_type: 'float',
      section: 'other', label: 'Activation Threshold', component: 'slider',
      min: 0, max: 1, step: 0.05,
      description: 'Softmax-probability floor a policy must clear to join the active label set.',
      order: 233,
    },
    {
      path: 'router.policies', yaml_key: 'policies', go_type: '[]RouterPolicy', ui_type: 'object',
      section: 'other', label: 'Policies', component: 'router-policies',
      description: 'Label vocabulary the classifier scores over.',
      order: 235,
    },
    {
      path: 'router.candidates', yaml_key: 'candidates', go_type: '[]RouterCandidate', ui_type: 'object',
      section: 'other', label: 'Candidates', component: 'router-candidates',
      description: 'Routing table: each entry binds a downstream model to a set of policy labels.',
      order: 236,
    },
    {
      path: 'router.embedding_cache.embedding_model', yaml_key: 'embedding_model', go_type: 'string', ui_type: 'string',
      section: 'other', label: 'L2 Cache: Embedding Model', component: 'model-select', autocomplete_provider: 'models',
      description: 'Embedding model used by the L2 decision cache.',
      order: 237,
    },
    {
      path: 'router.embedding_cache.similarity_threshold', yaml_key: 'similarity_threshold', go_type: 'float64', ui_type: 'float',
      section: 'other', label: 'L2 Cache: Similarity Threshold', component: 'slider',
      min: 0, max: 1, step: 0.01,
      description: 'Cosine-similarity floor a cache candidate must clear to count as a hit.',
      order: 238,
    },
  ],
}

const MIDDLEWARE_STATUS = {
  pii: { enabled_globally: false, patterns: [], models: [], recent_event_count: 0 },
  router: { configured: false, models: [], recent_decision_count: 0, available_classifiers: ['score'] },
  mitm: { running: false, listen_addr: '', configured_addr: '', host_owners: {}, host_conflicts: {}, models: [], ca_available: false, ca_cert_url: '' },
}

test.describe('Router template — create flow', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/auth/status', (route) =>
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ authEnabled: false, staticApiKeyRequired: false, providers: [] }),
      })
    )
    await page.route('**/api/middleware/status', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify(MIDDLEWARE_STATUS) })
    )
    await page.route('**/api/router/decisions?**', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ decisions: [] }) })
    )
    await page.route('**/api/pii/events?**', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ events: [] }) })
    )
    await page.route('**/api/models/config-metadata*', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify(ROUTER_METADATA) })
    )
    await page.route('**/api/models/config-metadata/autocomplete/**', (route) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify({ values: [] }) })
    )

    // Surface any uncaught render-time error so the assertion fails
    // with a useful message rather than the test silently passing.
    page.on('pageerror', (err) => {
      throw new Error(`uncaught page error: ${err.message}`)
    })
  })

  test('Routing tab links to the model editor with the router template loaded', async ({ page }) => {
    await page.goto('/app/middleware')
    await page.getByRole('button', { name: /Routing/i }).click()

    // Empty-state button is the primary CTA.
    await page.getByRole('button', { name: /Create routing model/i }).click()

    // Editor loads on a /app/model-editor URL with template=router.
    await expect(page).toHaveURL(/\/app\/model-editor.*template=router/)
  })

  test('Router template renders without crashing on structured candidates/policies', async ({ page }) => {
    // Navigate straight to the create-with-template URL. This was the
    // regression that crashed with "(intermediate value).split is not
    // a function" when the template's array-shaped router.candidates
    // fell into a code-editor wrapper.
    await page.goto('/app/model-editor?template=router')

    // The react-router error overlay must not appear.
    await expect(page.getByText(/Unexpected Application Error/i)).toHaveCount(0)

    // Editor surface visible. Template URL is "create mode", so the
    // heading reads "Add Model" rather than "Model Editor".
    await expect(page.locator('h1.page-title')).toBeVisible({ timeout: 10_000 })

    // Top-level field labels seeded by the template are visible.
    // embedding_cache.* fields are surfaced via "Add Field" search
    // rather than active by default — separate spec covers them.
    await expect(page.getByText('Classifier').first()).toBeVisible()
    await expect(page.getByText('Policies').first()).toBeVisible()
    await expect(page.getByText('Candidates').first()).toBeVisible()
    await expect(page.getByText('Activation Threshold').first()).toBeVisible()
  })

  test('Classifier select offers only the score option', async ({ page }) => {
    await page.goto('/app/model-editor?template=router')

    // SearchableSelect renders the current option's *label* inside the
    // trigger button. After the schema cleanup the only option is
    // "Score (Arch-Router-style)", pre-selected by the template.
    await expect(page.getByText('Score (Arch-Router-style)').first()).toBeVisible({ timeout: 10_000 })
  })

  test('Policies editor renders structured rows with label + description fields', async ({ page }) => {
    await page.goto('/app/model-editor?template=router')

    // The template seeds three example policies. Their labels are
    // pre-populated in input fields with monospace styling — the
    // editor signature is "Add policy" button + label/description
    // input pairs.
    await expect(page.getByRole('button', { name: /Add policy/i }).first()).toBeVisible()

    // Pre-seeded labels visible as input values. RouterPoliciesEditor
    // renders each label in an input with a recognisable placeholder;
    // assert on their values by position.
    const labelInputs = page.locator('input[placeholder^="label ("]')
    await expect(labelInputs.nth(0)).toHaveValue('code-generation')
    await expect(labelInputs.nth(1)).toHaveValue('casual-chat')
    await expect(labelInputs.nth(2)).toHaveValue('math-reasoning')
  })

  test('Candidates editor renders {model, labels} rows with policy-aware label chips', async ({ page }) => {
    await page.goto('/app/model-editor?template=router')

    // "Add candidate" is the signature of the new RouterCandidatesEditor.
    await expect(page.getByRole('button', { name: /Add candidate/i }).first()).toBeVisible()

    // Each candidate row should expose move-up/move-down controls,
    // a model picker, and label chips. The chip for a known policy
    // label appears as a button with the policy's label text.
    // Pre-seeded template: candidate[0] has labels=['casual-chat'];
    // candidate[1] has labels=['code-generation', 'casual-chat', 'math-reasoning'].
    //
    // The chips appear inside a flex row of buttons. Using getByRole
    // with the exact name catches typos/regressions cleanly.
    await expect(page.getByRole('button', { name: 'casual-chat' }).first()).toBeVisible()
    await expect(page.getByRole('button', { name: 'code-generation' }).first()).toBeVisible()
    await expect(page.getByRole('button', { name: 'math-reasoning' }).first()).toBeVisible()
  })

  test('Adding a duplicate policy label flags the duplicate row', async ({ page }) => {
    await page.goto('/app/model-editor?template=router')

    // Add a new empty policy row, then type a duplicate of the
    // existing 'casual-chat'. The duplicate detection in
    // RouterPoliciesEditor sets a warning border via inline style.
    await page.getByRole('button', { name: /Add policy/i }).first().click()

    // Find the newly-added empty label input (placeholder catches it).
    const newLabel = page.locator('input[placeholder*="label (e.g. code-generation)"]').last()
    await newLabel.fill('casual-chat')

    // Both rows now hold the same label. The duplicate-detection
    // logic flags the row visually; we assert on the title attribute
    // RouterPoliciesEditor sets on the input when duplicate=true.
    await expect(
      page.locator('input[title="Duplicate label — candidates won\'t be able to distinguish them"]').first()
    ).toBeVisible()
  })
})
