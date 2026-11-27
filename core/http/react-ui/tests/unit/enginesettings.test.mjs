import test from 'node:test'
import assert from 'node:assert/strict'
import {
  normalizeSchema, groupItems, readItem, writeItem, validate, readOption, writeOption, writeEntry,
  readEntry, readCombined, writeCombined, placementRequest, placementModes, choiceFromPlacement, patternOf,
  validateOverride, ownedOptionNames, rawOptions, replaceRawOptions, validateRawOption,
  rawOptionFor, applyPreset, applyParsed, describeChanges, undoChanges, diffPaths, unflatten,
  optionNames, hasCapability, readProfile, withProfile, resolveProfile, CUSTOM_PROFILE, CONCURRENCY_SET,
} from '../../src/utils/engineSettings.js'
import { isAllLayers } from '../../src/utils/placement.js'
// The answers of the real handlers (pkg/enginesettings), saved for the specs.
import SCHEMA from '../../e2e/fixtures/engine-llamacpp-schema.json' with { type: 'json' }
import INKLING from '../../e2e/fixtures/engine-llamacpp-parse-inkling.json' with { type: 'json' }
import MIXED from '../../e2e/fixtures/engine-llamacpp-parse-mixed.json' with { type: 'json' }

const schema = normalizeSchema(SCHEMA)
const items = schema.items
const byId = Object.fromEntries(items.map(i => [i.id, i]))
const loadMode = schema.combined.find(c => c.id === 'load_mode')
const preset = (id) => schema.presets.find(p => p.id === id)
const OT = schema.placement.names

// A second engine with a table of its own and no capability.
const TOY = normalizeSchema({
  id: 'toy-engine', name: 'Toy', capabilities: [], groups: ['limits'],
  flags: [
    { key: 'max_model_len', target: 'config', path: 'context_size', type: 'int', min: 1, doc: 'x', group: 'limits' },
    { key: 'eager', target: 'option', path: 'options', option: 'enforce_eager', option_aliases: ['eager'], type: 'bool', doc: 'y', group: 'limits' },
  ],
})

test('engine settings: the schema becomes settings with ids, types and flags', () => {
  assert.equal(schema.id, 'llama-cpp')
  assert.equal(schema.binary, 'llama-server')
  assert.equal(byId.context_size.type, 'int')
  assert.equal(byId.context_size.flag, '--ctx-size')
  assert.equal(byId.context_size.name, 'context_size')
  assert.equal(byId['options.ubatch'].option, 'ubatch')
  assert.equal(byId['options.ubatch'].flag, '-ub')
  assert.deepEqual(byId['options.ubatch'].names, ['ubatch', 'n_ubatch'])
  assert.equal(byId.flash_attention.type, 'enum')
  assert.deepEqual(byId.flash_attention.enum, ['on', 'off', 'auto'])
  assert.equal(byId['parameters.temperature'].type, 'float')
  assert.equal(byId['parameters.temperature'].key, 'parameters.temperature')
  assert.equal(byId['options.use_jinja'].group, 'template')
  assert.equal(byId.threads.group, 'speed')
  assert.ok(byId.mmlock)
  assert.ok(byId['options.-ot'])
  assert.ok(hasCapability(schema, 'command-line'))
  assert.ok(hasCapability(schema, 'tensor-placement'))
})

test('engine settings: the list form and messy entries still read', () => {
  const got = normalizeSchema([{ key: 'threads', type: 'integer', group: 'Memory & speed', flag: '--threads' }, { option: 'x', type: 'bool' }, { type: 'int' }, null])
  assert.equal(got.items.length, 2)
  assert.equal(got.items[0].type, 'int')
  assert.equal(got.items[0].flag, '--threads')
  assert.equal(got.items[0].group, 'memory_speed')
  assert.equal(got.items[1].id, 'options.x')
  assert.equal(got.placement, null)
  assert.deepEqual(normalizeSchema(null).items, [])
})

test('engine settings: blocks follow the schema groups; composite settings are left out', () => {
  const blocks = groupItems(schema)
  assert.deepEqual(blocks.map(g => g.id), ['memory', 'speed', 'serving', 'placement', 'sampling', 'template', 'speculative', 'engine_advanced'])
  // The rarely used and risky settings are drawn closed.
  assert.deepEqual(blocks.filter(g => g.collapsed).map(g => g.id), ['engine_advanced'])
  // The speculative group is the Drafting section, drawn open.
  assert.equal(blocks.find(g => g.id === 'speculative').drafting, true)
  const advanced = blocks.find(g => g.id === 'engine_advanced').items.map(i => i.id)
  for (const id of ['options.context_shift', 'options.kv_unified', 'options.cont_batching', 'options.backend_sampling_min_slots']) assert.ok(advanced.includes(id), id)
  assert.equal(blocks[3].placement, true)
  assert.deepEqual(blocks[2].items.map(i => i.id), [
    'options.parallel', 'options.paged_kv', 'options.paged_preempt', 'options.prefill_budget', 'options.prefill_long', 'options.kv_pool_tokens',
    'options.kv_output_reserve', 'options.slot_attach', 'options.admission_order', 'options.prefill_cap', 'options.lazy_idle_save', 'options.cache_idle_slots', 'options.n_cache_reuse', 'options.slot_prompt_similarity', 'options.kv_unified_per_slot',
  ])
  // An alias in a file reads into its field, and a write uses the canonical name.
  const unified = byId['options.kv_unified']
  assert.equal(readItem({ options: ['unified_kv:false'] }, unified), false)
  const aliased = { options: ['unified_kv:false', 'paged_serving:true', 'max_prefill_tokens:auto'] }
  writeItem(aliased, unified, true)
  assert.deepEqual(aliased.options, ['kv_unified:true', 'paged_serving:true', 'max_prefill_tokens:auto'])
  assert.equal(readItem(aliased, byId['options.prefill_budget']), 'auto')
  assert.equal(readItem(aliased, byId['options.paged_kv']), true)
  const memory = blocks[0].items.map(i => i.id)
  assert.ok(memory.includes('context_size'))
  assert.ok(!memory.includes('mmap'))
  assert.ok(!memory.includes('options.direct_io'))
  assert.deepEqual(blocks[0].combined.map(c => c.id), ['load_mode'])
  assert.ok(!blocks.flatMap(g => g.items).some(i => i.id === 'options.-ot'))
})

test('engine settings: options are read and written by name, keeping other entries', () => {
  const ub = optionNames(schema, 'ubatch')
  const opts = ['a:1', 'n_ubatch:32', 'b']
  assert.equal(readOption(opts, ub), '32')
  assert.equal(readOption(opts, ['b']), null)
  assert.equal(readOption(opts, ['c']), undefined)
  assert.deepEqual(writeOption(opts, ub, '64'), ['a:1', 'ubatch:64', 'b'])
  assert.deepEqual(writeOption(opts, ub, undefined), ['a:1', 'b'])
  assert.deepEqual(writeOption(['n_ubatch:1', 'ubatch:2'], ub, '3'), ['ubatch:3'])
  assert.equal(writeOption(['ubatch:1'], ub, undefined), undefined)
  assert.deepEqual(writeEntry(['override_tensor:a=CPU', 'x'], OT, '-ot:b=CPU'), ['-ot:b=CPU', 'x'])
  assert.equal(readEntry(['x', '-ot:b=CPU'], optionNames(schema, 'override_tensor')), '-ot:b=CPU')
})

test('engine settings: a setting reads and writes its key or its option', () => {
  const values = { context_size: 4096, options: ['fit:off', 'use_jinja', 'no_op_offload'] }
  assert.equal(readItem(values, byId.context_size), 4096)
  assert.equal(readItem(values, byId['options.fit']), 'off')
  assert.equal(readItem(values, byId['options.use_jinja']), true)
  assert.equal(readItem(values, byId['options.no_op_offload']), true)
  assert.equal(readItem(values, byId.threads), undefined)
  const draft = structuredClone(values)
  writeItem(draft, byId.threads, 6)
  writeItem(draft, byId['options.fit'], undefined)
  writeItem(draft, byId['parameters.temperature'], 0.7)
  assert.deepEqual(draft, { context_size: 4096, threads: 6, options: ['use_jinja', 'no_op_offload'], 'parameters.temperature': 0.7 })
  writeItem(draft, byId['options.use_jinja'], undefined)
  writeItem(draft, byId['options.no_op_offload'], undefined)
  assert.equal('options' in draft, false)
})

test('engine settings: typed text is validated against type and bounds', () => {
  assert.deepEqual(validate(byId.context_size, ''), { ok: true, value: undefined })
  assert.deepEqual(validate(byId.context_size, '8192'), { ok: true, value: 8192 })
  assert.equal(validate(byId.context_size, '1.5').error, 'notInteger')
  assert.equal(validate(byId.context_size, 'abc').error, 'notInteger')
  assert.equal(validate(byId['parameters.temperature'], 'x').error, 'notNumber')
  assert.equal(validate(byId['parameters.top_p'], '1.5').error, 'range')
  assert.equal(validate(byId.context_size, '-1').error, 'min')
  assert.equal(validate(byId.flash_attention, 'maybe').error, 'enum')
  assert.equal(validate({ id: 'x', type: 'int', max: 4 }, '5').error, 'max')
})

test('engine settings: the load mode folds mmap, mlock and direct I/O', () => {
  assert.equal(readCombined(schema, {}, loadMode), 'default')
  for (const mode of ['none', 'mmap', 'mlock', 'mmap_mlock', 'direct_io', 'default']) {
    const draft = { options: ['x:1'] }
    writeCombined(schema, draft, loadMode, mode)
    assert.equal(readCombined(schema, draft, loadMode), mode, mode)
  }
  // As the server's parser writes --load-mode none.
  const none = { mmlock: true }
  writeCombined(schema, none, loadMode, 'none')
  assert.deepEqual(none, { mmap: false })
  const direct = { mmap: false }
  writeCombined(schema, direct, loadMode, 'direct_io')
  assert.deepEqual(direct, { options: ['direct_io:true'] })
})

// The rule of the combined control gives what the old fixed rule gave for
// every mix of mmap, mlock and direct I/O a file can hold.
test('engine settings: the load mode reads every mix of the three settings', () => {
  const old = (mmap, mlock, direct) => {
    if (direct === true) return 'direct_io'
    if (mmap === undefined && mlock === undefined && direct === undefined) return 'default'
    const m = mmap ?? true
    const l = mlock ?? false
    if (m && l) return 'mmap_mlock'
    if (l) return 'mlock'
    if (m) return 'mmap'
    return 'none'
  }
  const states = [undefined, true, false]
  for (const mmap of states) {
    for (const mlock of states) {
      for (const direct of states) {
        const values = {}
        if (mmap !== undefined) values.mmap = mmap
        if (mlock !== undefined) values.mmlock = mlock
        if (direct !== undefined) values.options = [`direct_io:${direct}`]
        assert.equal(readCombined(schema, values, loadMode), old(mmap, mlock, direct), JSON.stringify(values))
      }
    }
  }
})

test('engine settings: tensor placement choices become server requests', () => {
  assert.equal(placementRequest({ mode: 'off' }), null)
  assert.equal(placementRequest({ mode: 'raw', raw: 'x=CPU' }), null)
  assert.deepEqual(placementRequest({ mode: 'experts' }), { placement: { mode: 'experts_cpu_all' } })
  assert.deepEqual(placementRequest({ mode: 'ranges', ranges: [{ from: '0', to: '6' }, { from: 18, to: 23 }] }),
    { placement: { mode: 'experts_cpu_layers', layers: [{ from: 0, to: 6 }, { from: 18, to: 23 }] } })
  assert.equal(placementRequest({ mode: 'ranges', ranges: [{ from: 5, to: 2 }] }).error, 'layerRange')
  assert.equal(placementRequest({ mode: 'ranges', ranges: [{ from: '', to: 2 }] }).error, 'layerRange')
  assert.deepEqual(placementRequest({ mode: 'rest', gpuLayers: '8', totalLayers: '' }), { placement: { mode: 'first_layers_gpu', first_gpu_layers: 8 } })
  assert.deepEqual(placementRequest({ mode: 'rest', gpuLayers: 8, totalLayers: '24' }), { placement: { mode: 'first_layers_gpu', first_gpu_layers: 8, total_layers: 24 } })
  assert.equal(placementRequest({ mode: 'rest', gpuLayers: 'x' }).error, 'notInteger')
  assert.deepEqual(placementModes(schema.placement), ['off', 'experts', 'ranges', 'rest', 'raw'])
  assert.deepEqual(placementModes({ modes: ['experts_cpu_all'] }), ['off', 'experts', 'raw'])
})

test('engine settings: a placement the server read back becomes a choice', () => {
  const { placements } = INKLING
  assert.deepEqual(choiceFromPlacement(placements[0].placement), { mode: 'ranges', ranges: [{ from: 0, to: 6 }, { from: 18, to: 23 }] })
  assert.deepEqual(choiceFromPlacement({ mode: 'experts_cpu_all' }), { mode: 'experts' })
  assert.deepEqual(choiceFromPlacement({ mode: 'first_layers_gpu', first_gpu_layers: 4 }), { mode: 'rest', gpuLayers: 4, totalLayers: '' })
  assert.deepEqual(choiceFromPlacement(null, 'token_embd=CPU'), { mode: 'raw', raw: 'token_embd=CPU' })
  assert.deepEqual(choiceFromPlacement(null, undefined), { mode: 'off' })
  assert.equal(patternOf(schema.placement, '-ot:blk\\.1\\.x=CPU'), 'blk\\.1\\.x=CPU')
  assert.equal(patternOf(schema.placement, 'override_tensor:a=CPU'), 'a=CPU')
  // Only the spellings the server reads back are sent to it.
  assert.deepEqual(schema.placement.recognised, ['-ot', '--override-tensor'])
})

test('engine settings: a placement value typed by hand is checked', () => {
  assert.equal(validateOverride(''), null)
  assert.equal(validateOverride('blk\\.[0-9]+\\.ffn_.*_exps.*=CPU,token_embd=CUDA0'), null)
  assert.equal(validateOverride('no-target').error, 'overrideShape')
  assert.equal(validateOverride('blk\\.(=CPU').error, 'overrideRegex')
  assert.equal(validateOverride('x=C P U').error, 'overrideTarget')
})

test('engine settings: raw options leave out what the form owns', () => {
  const owned = ownedOptionNames(schema)
  const opts = ['parallel:1', 'cpu_moe:true', '-ot:x=CPU', 'override_tensor:y=CPU', 'my_option:true', '--foo:bar']
  assert.deepEqual(rawOptions(opts, owned), ['my_option:true', '--foo:bar'])
  assert.deepEqual(replaceRawOptions(opts, owned, ['my_option:false']),
    ['parallel:1', 'cpu_moe:true', '-ot:x=CPU', 'override_tensor:y=CPU', 'my_option:false'])
  assert.deepEqual(replaceRawOptions(['a:1'], owned, ['a:1', 'b:2']), ['a:1', 'b:2'])
  assert.equal(replaceRawOptions(['a:1'], owned, []), undefined)
  assert.equal(validateRawOption('  ', owned).error, 'empty')
  assert.equal(validateRawOption('n_parallel:2', owned).error, 'owned')
  assert.equal(validateRawOption('-ot:a=CPU', owned).error, 'owned')
  assert.equal(validateRawOption('my_option:true', owned), null)
  assert.equal(rawOptionFor({ flag: '--rope-scale', value: '2' }), '--rope-scale:2')
  assert.equal(rawOptionFor({ flag: 'no-warmup', value: '' }), '--no-warmup')
})

test('engine settings: a preset fills settings, lists them and undoes them', () => {
  const before = { context_size: 8192, options: ['my_option:true'] }
  const draft = structuredClone(before)
  const bigMoe = preset('bigMoe')
  assert.deepEqual(bigMoe.placement, { mode: 'experts_cpu_all' })
  applyPreset(schema, draft, bigMoe, '-ot:\\.ffn_.*_exps.*=CPU')
  assert.equal(draft.flash_attention, 'on')
  // "All layers" is stored as the short value, not the 99999999 sentinel, and still reads as all layers.
  assert.equal(draft.gpu_layers, 999)
  assert.equal(isAllLayers(draft.gpu_layers), true)
  assert.equal(isAllLayers(99999999), true)
  assert.equal(isAllLayers(998), false)
  assert.equal(readEntry(draft.options, OT), '-ot:\\.ffn_.*_exps.*=CPU')
  assert.ok(draft.options.includes('my_option:true'))
  assert.ok(draft.options.includes('parallel:1'))
  const changes = describeChanges(schema, before, draft)
  const ids = changes.map(c => c.id)
  assert.ok(ids.includes('flash_attention'))
  assert.ok(ids.includes('options.-ot'))
  assert.ok(!ids.includes('context_size'))
  // An edit after the preset survives its undo.
  draft.cache_type_k = 'f16'
  undoChanges(draft, changes)
  assert.equal(draft.cache_type_k, 'f16')
  assert.equal(draft.flash_attention, undefined)
  assert.deepEqual(draft.options, ['my_option:true'])
  // A preset with settings the schema lacks writes only what it has.
  const bare = {}
  applyPreset({ ...schema, items: [byId.context_size] }, bare, preset('lowVram'))
  assert.deepEqual(bare, { context_size: 4096 })
})

test('engine settings: the quick setup profiles are apart from the presets', () => {
  assert.deepEqual(schema.profiles.map(p => p.id), ['balanced', 'longPrompts'])
  assert.deepEqual(schema.concurrency.map(p => p.id), ['concurrencyDefault', 'streams8', 'streams16', 'streams32'])
  assert.deepEqual(schema.concurrency.map(p => p.streams), [0, 8, 16, 32])
  assert.ok(schema.concurrency.every(p => p.set === CONCURRENCY_SET && p.bestFor))
  assert.equal(schema.memory.fallback, 'discrete')
  assert.equal(schema.memory.fitFraction, 0.9)
  assert.deepEqual(schema.memory.kinds.map(k => k.kind), ['unified', 'discrete', 'system'])
  assert.deepEqual(schema.presets.map(p => p.id), ['bigMoe', 'maxContext', 'lowVram'])
  assert.ok(schema.profiles.every(p => p.profile && p.placement === undefined))
  assert.deepEqual(schema.estimate, {
    batch: 'parameters.batch', ubatch: 'options.ubatch', cache_type_k: 'cache_type_k', cache_type_v: 'cache_type_v',
    context_size: 'context_size', gpu_layers: 'gpu_layers',
  })
  // The paged serving options have no llama-server flag.
  for (const id of ['options.paged_kv', 'options.paged_preempt', 'options.prefill_budget']) {
    assert.equal(byId[id].localAIOnly, true, id)
    assert.equal(byId[id].flag, undefined, id)
    assert.equal(byId[id].group, 'serving', id)
  }
  assert.equal(byId['options.parallel'].group, 'serving')
  // GPU sampling: a three-way choice over one plain options entry, Off writing none.
  const gpu = byId['options.backend_sampling']
  assert.equal(gpu.localAIOnly, true)
  assert.equal(gpu.flag, undefined)
  assert.ok(!groupItems(schema).flatMap(g => g.items).includes(gpu))
  const sampling = schema.combined.find(c => c.id === 'backend_sampling')
  assert.equal(sampling.group, 'speed')
  assert.deepEqual(sampling.choices.map(c => c.value), ['off', 'auto', 'on'])
  assert.match(sampling.choices[1].doc, /^Use GPU sampling when the model runs on a GPU/)
  assert.match(sampling.choices[2].doc, /^Sample tokens on the GPU\. Faster with many parallel requests\./)
  // Off is the backend default, so a file without the option reads as Off and Off writes nothing.
  assert.equal(readCombined(schema, {}, sampling), 'off')
  assert.equal(readCombined(schema, { options: ['backend_sampling:auto'] }, sampling), 'auto')
  const draft = { options: ['my_option:true'] }
  writeCombined(schema, draft, sampling, 'on')
  assert.deepEqual(draft.options, ['my_option:true', 'backend_sampling:on'])
  writeCombined(schema, draft, sampling, 'auto')
  assert.deepEqual(draft.options, ['my_option:true', 'backend_sampling:auto'])
  writeCombined(schema, draft, sampling, 'off')
  assert.deepEqual(draft.options, ['my_option:true'])
  // No batch profile touches it; the 16 and 32 stream setups do.
  assert.ok(schema.profiles.every(p => !('options.backend_sampling' in p.settings)))
  assert.equal(schema.concurrency.find(p => p.id === 'streams16').settings['options.backend_sampling'], 'on')
})

test('engine settings: a batch profile is read back from the values, per memory kind, else custom', () => {
  const prof = (id, kind = 'unified') => resolveProfile(schema.profiles.find(p => p.id === id), kind)
  assert.equal(readProfile(schema, {}), 'balanced')
  // Settings no profile lists do not matter.
  assert.equal(readProfile(schema, { context_size: 8192, options: ['use_jinja:true'] }), 'balanced')
  const long = withProfile(schema, { context_size: 8192, options: ['use_jinja:true'] }, prof('longPrompts'))
  assert.equal(long['parameters.batch'], 2048)
  assert.deepEqual(long.options, ['use_jinja:true', 'ubatch:2048'])
  assert.equal(readProfile(schema, long, { kind: 'unified' }), 'longPrompts')
  // A value written as text in the file still matches.
  assert.equal(readProfile(schema, { 'parameters.batch': '2048', options: ['ubatch:2048'] }, { kind: 'unified' }), 'longPrompts')
  // The micro-batch of Long prompts depends on the memory kind.
  assert.deepEqual(withProfile(schema, {}, prof('longPrompts', 'discrete')).options, ['ubatch:1024'])
  assert.deepEqual(withProfile(schema, {}, prof('longPrompts', 'system')).options, ['ubatch:512'])
  assert.equal(readProfile(schema, long, { kind: 'discrete' }), CUSTOM_PROFILE)
  // Back to Balanced unsets the batch sizes and keeps the rest.
  assert.deepEqual(withProfile(schema, long, prof('balanced')), { context_size: 8192, options: ['use_jinja:true'] })
  // Batch profiles leave the concurrency settings alone.
  const busy = { options: ['parallel:16', 'paged_kv:true'] }
  assert.deepEqual(withProfile(schema, busy, prof('longPrompts')).options, ['parallel:16', 'paged_kv:true', 'ubatch:2048'])
  // A setting off every profile is custom.
  assert.equal(readProfile(schema, { 'parameters.batch': 1024 }), CUSTOM_PROFILE)
  // withProfile leaves its input alone.
  assert.equal(long.cache_type_k, undefined)
})

test('engine settings: concurrency profiles are a set of their own, with values per memory kind', () => {
  const tier = (id, kind, tight = false) => resolveProfile(schema.concurrency.find(p => p.id === id), kind, tight)
  const set = { set: CONCURRENCY_SET }
  assert.equal(readProfile(schema, {}, { ...set, kind: 'discrete' }), 'concurrencyDefault')
  // 8 streams: the same on a discrete GPU, plus the host cache bounds on unified memory.
  const d8 = withProfile(schema, { options: ['use_jinja:true'] }, tier('streams8', 'discrete'))
  assert.deepEqual([...d8.options].sort(), ['paged_kv:true', 'paged_preempt:true', 'parallel:8', 'use_jinja:true'])
  assert.equal(readProfile(schema, d8, { ...set, kind: 'discrete' }), 'streams8')
  const u8 = withProfile(schema, {}, tier('streams8', 'unified'))
  assert.ok(u8.options.includes('cache_ram:4096'))
  assert.ok(!u8.options.some(o => o.startsWith('n_ctx_checkpoints')))
  assert.equal(readProfile(schema, u8, { ...set, kind: 'unified' }), 'streams8')
  // The batch choice does not see the concurrency settings.
  assert.equal(readProfile(schema, u8), 'balanced')
  // 16 streams: GPU sampling on and drafting off, the draft model removed.
  const d16 = withProfile(schema, { draft_model: 'small.gguf', options: ['spec_type:draft-mtp'] }, tier('streams16', 'discrete'))
  assert.equal(d16.draft_model, undefined)
  assert.ok(d16.options.includes('spec_type:none'))
  assert.ok(d16.options.includes('backend_sampling:on'))
  // No GPU: no GPU sampling.
  assert.ok(!withProfile(schema, {}, tier('streams16', 'system')).options.some(o => o.startsWith('backend_sampling')))
  // 32 streams: q8_0 on a discrete GPU; f16 on unified memory unless tight.
  const d32 = withProfile(schema, {}, tier('streams32', 'discrete'))
  assert.equal(d32.cache_type_k, 'q8_0')
  assert.equal(d32.flash_attention, 'on')
  assert.ok(d32.options.includes('backend_sampling:on'))
  assert.equal(withProfile(schema, {}, tier('streams32', 'unified')).cache_type_k, undefined)
  const tight = tier('streams32', 'unified', true)
  assert.equal(tight.tight, true)
  const u32 = withProfile(schema, {}, tight)
  assert.equal(u32.cache_type_v, 'q8_0')
  // The tight values still read back as 32+ streams.
  assert.equal(readProfile(schema, u32, { ...set, kind: 'unified' }), 'streams32')
  // Down from 32 to 16 removes what 32 wrote and 16 does not list.
  const down = withProfile(schema, d32, tier('streams16', 'discrete'))
  assert.equal(down.cache_type_k, undefined)
  assert.equal(down.flash_attention, undefined)
  assert.ok(down.options.includes('backend_sampling:on'))
  // Default removes every tier setting and keeps the person's own.
  const back = withProfile(schema, { ...d32, options: [...d32.options, 'use_jinja:true'] }, tier('concurrencyDefault', 'discrete'))
  assert.deepEqual(back, { options: ['use_jinja:true'] })
  // A flash attention value the person set is kept by Default.
  assert.equal(withProfile(schema, { flash_attention: 'off', options: ['parallel:8', 'paged_kv:true', 'paged_preempt:true'] }, tier('concurrencyDefault', 'discrete')).flash_attention, 'off')
  // A slot count off every tier is custom.
  assert.equal(readProfile(schema, { options: ['parallel:4', 'paged_kv:true'] }, { ...set, kind: 'discrete' }), CUSTOM_PROFILE)
})

test('engine settings: the Inkling command fills every field', () => {
  const draft = { 'parameters.model': 'inkling.gguf', options: ['parallel:4', 'my_option:true'] }
  const { modelFile } = applyParsed(schema, draft, INKLING)
  assert.equal(modelFile, 'Inkling-2-Mini-UD-Q3_K_XL.gguf')
  assert.equal(draft['parameters.model'], 'inkling.gguf')
  const want = {
    context_size: 18000, threads: 6, gpu_layers: 99, flash_attention: 'on', cache_type_k: 'q8_0', cache_type_v: 'q8_0',
    'parameters.temperature': 1, 'parameters.top_p': 1, 'parameters.top_k': 0, 'parameters.min_p': 0,
    'options.use_jinja': true, 'options.parallel': 1, 'options.ubatch': 64, 'options.threads_batch': 12,
    'options.fit': 'off', 'options.no_op_offload': true,
  }
  for (const [id, v] of Object.entries(want)) assert.equal(readItem(draft, byId[id]), v, id)
  assert.equal(readCombined(schema, draft, loadMode), 'none')
  assert.equal(readEntry(draft.options, OT), INKLING.placements[0].option)
  assert.equal(draft.options.filter(e => e.startsWith('parallel')).length, 1)
  assert.ok(draft.options.includes('my_option:true'))
  assert.deepEqual(INKLING.unmapped, [])
  // Nothing the command set is left over as a raw option.
  assert.deepEqual(rawOptions(draft.options, ownedOptionNames(schema)), ['my_option:true'])
})

test('engine settings: flags with no field are kept as the server wrote them', () => {
  const draft = {}
  applyParsed(schema, draft, MIXED)
  assert.deepEqual(draft.options, ['--rope-freq-base:10000', '--no-perf'])
  assert.equal(draft.context_size, 4096)
})

test('engine settings: diffs and nesting for the editor and the preview', () => {
  assert.deepEqual(diffPaths({ a: 1, b: 2 }, { a: 1, c: 3 }), [['b', undefined], ['c', 3]])
  assert.deepEqual(unflatten({ 'parameters.top_k': 0, context_size: 1, gone: undefined }), { parameters: { top_k: 0 }, context_size: 1 })
})

test('engine settings: a second engine works from its schema alone', () => {
  assert.equal(TOY.placement, null)
  assert.equal(hasCapability(TOY, 'command-line'), false)
  assert.deepEqual(TOY.presets, [])
  assert.deepEqual(groupItems(TOY).map(g => [g.id, g.items.map(i => i.id)]), [['limits', ['context_size', 'options.enforce_eager']]])
  const values = { options: ['eager:true', 'seed:3'] }
  const eager = TOY.items[1]
  assert.equal(readItem(values, eager), true)
  const draft = structuredClone(values)
  writeItem(draft, eager, false)
  assert.deepEqual(draft.options, ['enforce_eager:false', 'seed:3'])
  assert.deepEqual(rawOptions(draft.options, ownedOptionNames(TOY)), ['seed:3'])
  const changes = describeChanges(TOY, values, draft)
  assert.deepEqual(changes.map(c => c.id), ['options.enforce_eager'])
})
