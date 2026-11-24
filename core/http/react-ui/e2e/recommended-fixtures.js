// The answer of GET /api/models/recommended, in the shape the server sends it
// (core/http/endpoints/localai/recommended_models.go), for the specs that
// draw the first-run recommendation or the "Best for this machine" shelf.

const GB = 1024 * 1024 * 1024

export const REC_MODELS = [
  {
    id: 'qwen3.8-4b-q4', name: 'Qwen3.8 4B', role: 'default',
    reason: 'The most capable model that fits a small card with a full chat window.',
    license: 'apache-2.0', context_size: 32768, memory_bytes: Math.round(5.1 * GB), size_bytes: Math.round(2.8 * GB),
    fit: 'fits', fit_reasons: [], installed: false,
  },
  {
    id: 'gemma-4-e4b-it-qat-q4_0', name: 'Gemma 4 E4B', role: 'alternative',
    reason: 'Efficient 4B-class Gemma 4 that also reads images.',
    license: 'apache-2.0', context_size: 24576, memory_bytes: Math.round(4.0 * GB), size_bytes: Math.round(5.2 * GB),
    fit: 'fits', fit_reasons: ['window_reduced'], installed: false,
  },
  {
    id: 'qwen3.8-2b-q4', name: 'Qwen3.8 2B', role: 'alternative',
    reason: 'Leaves most of the card free for other work.',
    license: 'apache-2.0', context_size: 32768, memory_bytes: Math.round(2.3 * GB), size_bytes: Math.round(1.3 * GB),
    fit: 'fits', fit_reasons: [], installed: false,
  },
]

// The CPU tier of the built-in list: Gemma 4 E2B first, then Qwen3.8 4B and
// LFM2.5 24B A2B, with the window and memory the estimator gives on a 16 GiB host.
export const CPU_REC_MODELS = [
  {
    id: 'gemma-4-e2b-it-qat-q4_0', name: 'Gemma 4 E2B', role: 'default',
    reason: "Google's smallest Gemma 4, built for devices without a GPU; it fits an 8 GB machine and reads images.",
    license: 'apache-2.0', context_size: 32768, memory_bytes: Math.round(4.75 * GB), size_bytes: Math.round(3.35 * GB),
    fit: 'fits', fit_reasons: [], installed: false,
  },
  {
    id: 'qwen3.8-4b-q4', name: 'Qwen3.8 4B', role: 'alternative',
    reason: 'A step up in quality for machines with more memory; it follows tools and long instructions.',
    license: 'apache-2.0', context_size: 32768, memory_bytes: Math.round(5.21 * GB), size_bytes: Math.round(2.78 * GB),
    fit: 'fits', fit_reasons: [], installed: false,
  },
  {
    id: 'lfm2.5-24b-a2b-ud-q4', name: 'LFM2.5 24B A2B', role: 'alternative',
    reason: 'A 24B mixture-of-experts model with about 2.45B active parameters, fast on a CPU when the host has 16 GiB or more of RAM.',
    license: 'other', context_size: 32768, memory_bytes: Math.round(16.4 * GB), size_bytes: Math.round(14.0 * GB),
    fit: 'fits', fit_reasons: [], installed: false,
  },
]

// recommendedAnswer builds an answer; pass fields to change it.
export function recommendedAnswer(over = {}) {
  return {
    tier: 'gpu-small',
    hardware: { detected: true, kind: 'gpu', memory_bytes: 6 * GB, scope: 'local' },
    models: REC_MODELS,
    nothing_fits: false,
    min_context: 16384,
    recommended_context: 32768,
    ...over,
  }
}

// mockRecommended answers the endpoint. It returns the requests it saw.
export async function mockRecommended(page, answer = recommendedAnswer()) {
  const seen = []
  await page.route('**/api/models/recommended', route => {
    seen.push(route.request().url())
    return route.fulfill({ json: answer })
  })
  return seen
}
