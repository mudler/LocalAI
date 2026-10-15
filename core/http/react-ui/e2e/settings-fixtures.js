// Fixtures for the Settings page: one settings object in the shape of
// GET /api/settings (with the Go duration form, "15m0s"), and one function that
// answers the calls the page makes and records what it is sent.
//
// Pure of Playwright imports so a script that is not a test can use it too.

export function settingsFixture(overrides = {}) {
  return {
    watchdog_enabled: true, watchdog_idle_enabled: true, watchdog_busy_enabled: false,
    watchdog_idle_timeout: '15m0s', watchdog_busy_timeout: '5m0s', watchdog_interval: '500ms',
    max_active_backends: 0, auto_upgrade_backends: false, prefer_development_backends: false,
    memory_reclaimer_enabled: true, memory_reclaimer_threshold: 0.9,
    force_eviction_when_busy: false, size_aware_eviction: false,
    lru_eviction_max_retries: 50, lru_eviction_retry_interval: '1s',
    threads: 8, context_size: 8192, artifact_download_concurrency: 4, vram_budget: '80%', f16: false, debug: false,
    enable_tracing: true, tracing_max_items: 500, tracing_max_body_bytes: 65536, enable_backend_logging: true,
    cors: true, csrf: false, cors_allow_origins: 'https://app.example.org',
    p2p_token: '', p2p_network_id: '', federated: false,
    galleries: [{ url: 'https://index.localai.io/models', name: 'localai' }],
    backend_galleries: [{ url: 'https://index.localai.io/backends', name: 'localai' }],
    autoload_galleries: true, autoload_backend_galleries: true, vram_persistent_cache: true,
    api_keys: ['sk-shared-1'], agent_job_retention_days: 30,
    open_responses_store_ttl: '0', agent_pool_enabled: true, agent_pool_default_model: 'qwen3-8b-instruct',
    agent_pool_embedding_model: 'bge-m3', agent_pool_max_chunking_size: 400, agent_pool_chunk_overlap: 0,
    agent_pool_enable_logs: false, agent_pool_collection_db_path: '', agent_pool_vector_engine: 'chromem',
    agent_pool_database_url: '', agent_pool_agent_hub_url: '',
    distributed_disk_headroom_check: true, localai_assistant_enabled: true,
    instance_name: 'Lab AI', instance_tagline: '',
    ...overrides,
  }
}

export const RESOURCES = { gpus: [{ used: 18.4e9, total: 24e9 }], ram: { used: 20e9, total: 64e9 } }

// Answers GET and POST /api/settings and /api/resources. `posts` collects the
// request bodies, in order. `failNext` makes the next POST answer 400.
export async function mockSettings(page, overrides = {}) {
  const state = { posts: [], failNext: null, current: settingsFixture(overrides) }
  await page.route('**/api/settings', async (route) => {
    const req = route.request()
    if (req.method() === 'GET') return route.fulfill({ json: state.current })
    const body = req.postDataJSON()
    state.posts.push(body)
    if (state.failNext) {
      const error = state.failNext
      state.failNext = null
      return route.fulfill({ status: 400, json: { success: false, error } })
    }
    state.current = { ...state.current, ...body }
    return route.fulfill({ json: { success: true, message: 'Settings updated successfully' } })
  })
  await page.route('**/api/resources', route => route.fulfill({ json: RESOURCES }))
  return state
}
