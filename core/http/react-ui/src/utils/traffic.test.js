import assert from 'node:assert/strict'
import test from 'node:test'

import {
  appendReading, barGeometry, bucketLabel, compactCount, durationText, failureWords, filterRows,
  filterTraces, groupUsage, keyRows, metricsReport, modelStats, niceMax, overviewFigures,
  parseMetricFamilies, percentGeometry, percentText, relatedOperations, requestModel, scrapeConfig,
  seriesByBucket, sortRows, stackedByGroup, toCSV, totalsOf, traceColumns, traceCounts, traceState,
  traceTime, windowById, projectTotals, quotaForecast,
} from './traffic.js'

const row = (extra) => ({ bucket: '2026-10-07', model: 'qwen', user_id: 'u1', user_name: 'Alice', prompt_tokens: 10, completion_tokens: 5, total_tokens: 15, request_count: 2, ...extra })

test('windows map to the usage period and to a trace window the API accepts', () => {
  assert.deepEqual(windowById('24h'), { id: '24h', period: 'day', hours: 24, capped: false })
  assert.equal(windowById('30d').hours, 168)
  assert.equal(windowById('30d').capped, true)
  assert.equal(windowById('nope').id, '24h')
})

test('formats counts, shares and durations without inventing precision', () => {
  assert.equal(compactCount(723), '723')
  assert.equal(compactCount(1500), '1.5k')
  assert.equal(compactCount(12_400), '12k')
  assert.equal(compactCount(2_000_000), '2M')
  assert.equal(percentText(0, 100), '0%')
  assert.equal(percentText(7, 723), '1.0%')
  assert.equal(percentText(1, 5000), '<0.1%')
  assert.equal(percentText(1, 0), null)
  assert.equal(durationText(0.4), '<1 ms')
  assert.equal(durationText(417), '417 ms')
  assert.equal(durationText(1400), '1.4 s')
  assert.equal(durationText(NaN), '-')
})

test('labels ledger buckets without shifting the day by a time zone', () => {
  assert.equal(bucketLabel('2026-10-07 14:00', 'day'), '14:00')
  assert.match(bucketLabel('2026-10-07', 'week', 'en-US'), /Oct 7/)
  assert.match(bucketLabel('2026-10', 'all', 'en-US'), /Oct 2026/)
})

test('groups ledger rows by model and by user and sums each', () => {
  const rows = [row(), row({ bucket: '2026-10-08', request_count: 3, total_tokens: 20 }), row({ model: 'bge', user_id: 'u2', user_name: 'Bob', total_tokens: 4, request_count: 1 })]
  const byModel = groupUsage(rows, 'model')
  assert.equal(byModel.find(r => r.id === 'qwen').requests, 5)
  assert.equal(byModel.find(r => r.id === 'qwen').total, 35)
  const byUser = groupUsage(rows, 'user')
  assert.deepEqual(byUser.map(r => r.name).sort(), ['Alice', 'Bob'])
  assert.deepEqual(totalsOf(rows), { requests: 6, prompt: 30, completion: 15, total: 39 })
})

test('key rows keep tokens and requests and leave the split empty', () => {
  const rows = keyRows({
    by_key: [{ api_key_id: 'k1', api_key_name: 'ci-bot', user_name: 'Alice', tokens: 900, requests: 12, last_used: '2026-10-07T10:00:00Z' }],
    by_source: { web: { tokens: 50, requests: 3 }, legacy: { tokens: 5, requests: 1 } },
  }, { showUser: true })
  assert.equal(rows[0].name, 'ci-bot')
  assert.equal(rows[0].sub, 'Alice')
  assert.equal(rows[0].prompt, null)
  assert.deepEqual(rows.slice(1).map(r => r.id), ['web', 'legacy'])
})

test('sorts rows both ways with the name as a tie-break and filters on name and sub', () => {
  const rows = [{ name: 'b', sub: '', requests: 1, total: 5 }, { name: 'a', sub: 'x', requests: 1, total: 5 }, { name: 'c', sub: '', requests: 9, total: 1 }]
  assert.deepEqual(sortRows(rows, { key: 'total', direction: 'desc' }).map(r => r.name), ['a', 'b', 'c'])
  assert.deepEqual(sortRows(rows, { key: 'requests', direction: 'asc' }).map(r => r.name), ['a', 'b', 'c'])
  assert.deepEqual(filterRows(rows, 'X').map(r => r.name), ['a'])
  assert.equal(filterRows(rows, '').length, 3)
})

test('series are oldest first and the stack folds the tail into Other', () => {
  const rows = [
    row({ bucket: '2026-10-08', model: 'a', total_tokens: 10 }),
    row({ bucket: '2026-10-07', model: 'a', total_tokens: 30 }),
    row({ bucket: '2026-10-07', model: 'b', total_tokens: 20 }),
    row({ bucket: '2026-10-07', model: 'c', total_tokens: 1 }),
    row({ bucket: '2026-10-07', model: 'd', total_tokens: 1 }),
  ]
  assert.deepEqual(seriesByBucket(rows).map(p => p.bucket), ['2026-10-07', '2026-10-08'])
  const stack = stackedByGroup(rows, 'model', 2)
  assert.deepEqual(stack.groups.map(g => g.name), ['a', 'b', 'Other'])
  assert.equal(stack.groups[0].series, 1)
  assert.equal(stack.groups[2].series, 'other')
  assert.equal(stack.points[0].values.__other__, 2)
})

test('trace columns split succeeded from failed and never go negative', () => {
  const cols = traceColumns({ buckets: [{ start: 't0', count: 10, errors: 3 }, { start: 't1', count: 1, errors: 4 }] })
  assert.deepEqual(cols, [{ start: 't0', ok: 7, failed: 3 }, { start: 't1', ok: 0, failed: 4 }])
  assert.deepEqual(traceColumns(null), [])
})

test('bar axes start at zero and top out on a round number above the tallest bar', () => {
  assert.equal(niceMax(0), 1)
  assert.equal(niceMax(37), 40)
  assert.equal(niceMax(100), 100)
  assert.equal(niceMax(101), 150)
  const g = barGeometry([
    { key: 'a', label: 'a', segments: [{ id: 'ok', value: 30 }, { id: 'failed', value: 10 }] },
    { key: 'b', label: 'b', segments: [{ id: 'ok', value: 0 }] },
  ], { width: 400, height: 200 })
  assert.equal(g.max, 40)
  assert.equal(g.ticks[0].value, 0)
  assert.equal(g.cols[0].segs.length, 2)
  assert.equal(g.cols[1].segs.length, 0)
  // The failed segment sits on top of the succeeded one.
  assert.ok(g.cols[0].segs[1].y < g.cols[0].segs[0].y)
})

test('the overview figure is null when its source cannot say, and p95 is never zero for an empty buffer', () => {
  const ledger = { requests: 723, prompt: 682000, completion: 234000 }
  const full = overviewFigures({ ledger, summary: { total: 700, errors: 7, p95_ms: 1400 }, tracing: true })
  assert.equal(full.failed, 7)
  assert.equal(full.failedShare, '1.0%')
  assert.equal(full.p95, 1400)
  const empty = overviewFigures({ ledger, summary: { total: 0, errors: 0, p95_ms: 0 }, tracing: true })
  assert.equal(empty.failed, 0)
  assert.equal(empty.p95, null)
  const off = overviewFigures({ ledger, summary: { total: 0, errors: 0, p95_ms: 0 }, tracing: false })
  assert.equal(off.failed, null)
  assert.equal(off.p95, null)
  assert.equal(off.requests, 723)
  const noLedger = overviewFigures({ ledger: null, summary: null, tracing: true })
  assert.equal(noLedger.requests, null)
  assert.equal(noLedger.failed, null)
})

test('model statistics join the ledger, the backend buffer and the loaded set', () => {
  const rows = modelStats({
    ledger: [{ id: 'qwen', name: 'qwen', requests: 4, prompt: 10, completion: 5, total: 15 }],
    backend: [
      { model_name: 'qwen', duration: 2_000_000_000, timestamp: '2026-10-07T10:00:00Z' },
      { model_name: 'qwen', duration: 4_000_000_000, error: 'oom', timestamp: '2026-10-07T11:00:00Z' },
      { model_name: 'qwen', duration: 9_000_000_000, status: 'running' },
      { model_name: 'bge', duration: 1_000_000_000 },
    ],
    loaded: [{ model_name: 'qwen', backend: 'llama-cpp', rss_bytes: 1024, cpu_percent: 3 }, { model_name: 'idle', backend: 'x' }],
  })
  const qwen = rows.find(r => r.name === 'qwen')
  assert.equal(qwen.operations, 3)
  assert.equal(qwen.failed, 1)
  assert.equal(qwen.lastError, 'oom')
  assert.equal(qwen.meanMs, 3000)
  assert.equal(qwen.loaded, true)
  assert.equal(qwen.rssBytes, 1024)
  const bge = rows.find(r => r.name === 'bge')
  assert.equal(bge.requests, 0)
  assert.equal(bge.loaded, false)
  assert.equal(bge.rssBytes, null)
  assert.equal(rows.find(r => r.name === 'idle').meanMs, null)
})

test('a trace is failed on a 5xx or an error, refused on a 4xx, running at status 0', () => {
  assert.equal(traceState({ response: { status: 200 } }), 'ok')
  assert.equal(traceState({ response: { status: 503 } }), 'failed')
  assert.equal(traceState({ response: { status: 200 }, error: 'boom' }), 'failed')
  assert.equal(traceState({ response: { status: 429 } }), 'refused')
  assert.equal(traceState({ response: { status: 0 } }), 'running')
})

test('the failure words come from the recorded status and error and add no advice', () => {
  assert.deepEqual(failureWords({ response: { status: 500 }, error: 'out of memory' }), { kind: 'error', status: 500, error: 'out of memory' })
  assert.deepEqual(failureWords({ response: { status: 502 } }), { kind: 'status', status: 502, error: '' })
  assert.equal(failureWords({ response: { status: 429 } }).kind, 'refused')
  assert.equal(failureWords({ response: { status: 200 } }), null)
})

test('reads a trace time in ISO or in nanoseconds', () => {
  assert.equal(traceTime('2026-10-07T10:00:00Z'), Date.parse('2026-10-07T10:00:00Z'))
  assert.equal(traceTime(1_700_000_000_000_000_000), 1_700_000_000_000)
  assert.equal(traceTime(''), null)
})

test('related operations are matched on time and model and are called a match', () => {
  const start = Date.parse('2026-10-07T10:00:00Z')
  const body = btoa(JSON.stringify({ model: 'qwen' }))
  const trace = { timestamp: '2026-10-07T10:00:00Z', duration: 3_000_000_000, request: { body } }
  assert.equal(requestModel(trace), 'qwen')
  const ops = relatedOperations(trace, [
    { id: 'a', type: 'model_load', model_name: 'qwen', timestamp: new Date(start + 100).toISOString(), duration: 2_000_000_000, error: 'oom' },
    { id: 'b', type: 'llm', model_name: 'other', timestamp: new Date(start + 200).toISOString(), duration: 1_000_000 },
    { id: 'c', type: 'llm', model_name: 'qwen', timestamp: new Date(start + 60_000).toISOString(), duration: 1_000_000 },
  ])
  assert.deepEqual(ops.map(o => o.id), ['a'])
  assert.equal(ops[0].offsetMs, 100)
  assert.equal(ops[0].failed, true)
  assert.deepEqual(relatedOperations({ request: {} }, []), [])
})

test('filters traces by state and text and counts each filter', () => {
  const traces = [
    { id: '1', duration: 4_200_000_000, request: { method: 'POST', path: '/v1/chat/completions' }, response: { status: 500 }, error: 'ctx' },
    { id: '2', duration: 100_000_000, request: { method: 'POST', path: '/v1/embeddings' }, response: { status: 200 }, user_name: 'bob' },
  ]
  assert.deepEqual(traceCounts(traces), { all: 2, failed: 1, slow: 1 })
  assert.deepEqual(filterTraces(traces, { state: 'failed' }).map(t => t.id), ['1'])
  assert.deepEqual(filterTraces(traces, { state: 'slow' }).map(t => t.id), ['1'])
  assert.deepEqual(filterTraces(traces, { query: 'BOB' }).map(t => t.id), ['2'])
})

test('csv quotes the cells that need it', () => {
  const csv = toCSV([{ label: 'Name', value: r => r.name }, { label: 'Tokens', value: r => r.total }], [{ name: 'a,b', total: 3 }, { name: 'say "x"', total: null }])
  assert.equal(csv, 'Name,Tokens\n"a,b",3\n"say ""x""",\n')
})

test('reads the metric families of a scrape and says which known ones are present', () => {
  const text = '# HELP api_call api calls\n# TYPE api_call histogram\napi_call_bucket{le="1"} 1\n# TYPE go_goroutines gauge\n# TYPE localai_tokens_total counter\n'
  const families = parseMetricFamilies(text)
  assert.equal(families.get('api_call'), 'histogram')
  const report = metricsReport(families)
  assert.equal(report.known.find(m => m.name === 'api_call').seen, true)
  assert.equal(report.known.find(m => m.name === 'localai_tokens_total').seen, true)
  assert.equal(report.known.find(m => m.name === 'localai_pii_events_total').seen, false)
  assert.deepEqual(report.other, ['go_goroutines'])
  assert.equal(metricsReport(null).known[0].seen, null)
})

test('the scrape config names the endpoint, the token placeholder and the target', () => {
  const config = scrapeConfig('gpu.example:8080')
  assert.match(config, /metrics_path: \/metrics/)
  assert.match(config, /credentials: <admin API key>/)
  assert.match(config, /'gpu.example:8080'/)
})

test('host readings are bounded and a missing reading is skipped, not stored as zero', () => {
  let samples = []
  samples = appendReading(samples, null, 1000)
  assert.equal(samples.length, 0)
  samples = appendReading(samples, 10, 1000)
  samples = appendReading(samples, 20, 1000)
  assert.equal(samples.length, 1)
  assert.equal(samples[0].value, 20)
  for (let i = 0; i < 10; i++) samples = appendReading(samples, i, 2000 + i, 5)
  assert.equal(samples.length, 5)
  assert.equal(percentGeometry([{ t: 1, value: 1 }]), null)
  const g = percentGeometry([{ t: 1, value: 0 }, { t: 2, value: 100 }])
  assert.equal(g.ticks[0].value, 0)
  assert.equal(g.ticks[2].value, 100)
  assert.ok(g.points[1].y < g.points[0].y)
})

test('the projection is a straight line, and absent where it has nothing to say', () => {
  const day = Array.from({ length: 12 }, (_, i) => ({ requests: i * 2, prompt: i, completion: i, total: i * 10 }))
  const projected = projectTotals(day, 'day')
  assert.ok(projected.total > day.reduce((s, p) => s + p.total, 0))
  assert.equal(projectTotals(day, 'all'), null)
  assert.equal(projectTotals(day.slice(0, 1), 'day'), null)
  assert.equal(projectTotals(Array.from({ length: 24 }, () => ({ requests: 1, prompt: 1, completion: 1, total: 1 })), 'day'), null)
})

test('a quota is within limits when the pace lasts until it resets', () => {
  const series = [{ requests: 10, total: 100 }, { requests: 10, total: 100 }]
  const soon = new Date(Date.now() + 3_600_000).toISOString()
  const far = new Date(Date.now() + 30 * 24 * 3_600_000).toISOString()
  const ok = quotaForecast([{ model: 'q', window: '1d', max_total_tokens: 100000, current_tokens: 0, resets_at: soon }], series, 'week')
  assert.equal(ok[0].items[0].within, true)
  const bad = quotaForecast([{ window: '30d', max_requests: 100, current_requests: 90, resets_at: far }], series, 'week')
  assert.equal(bad[0].items[0].within, false)
  assert.deepEqual(quotaForecast([], series, 'week'), [])
})
