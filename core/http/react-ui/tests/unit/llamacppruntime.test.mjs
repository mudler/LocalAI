import test from 'node:test'
import assert from 'node:assert/strict'
import {
  normalizeSchema, groupItems, readItem, writeItem, validate, readOption, writeOption, writeEntry,
  readEntry, readLoadMode, writeLoadMode, placementRequest, choiceFromPlacement, patternOf,
  validateOverride, ownedOptionNames, rawOptions, replaceRawOptions, validateRawOption,
  rawOptionFor, applyPreset, SETUP_PRESETS, applyParsed, describeChanges, undoChanges,
  diffPaths, unflatten, TENSOR_ITEM,
} from '../../src/utils/llamaCppRuntime.js'
// The answers of the real handlers (pkg/llamacppsettings), saved for the specs.
import SCHEMA from '../../e2e/fixtures/llamacpp-settings-schema.json' with { type: 'json' }
import INKLING from '../../e2e/fixtures/llamacpp-parse-inkling.json' with { type: 'json' }
import MIXED from '../../e2e/fixtures/llamacpp-parse-mixed.json' with { type: 'json' }

const items = normalizeSchema(SCHEMA)
const byId = Object.fromEntries(items.map(i => [i.id, i]))

test('llama.cpp runtime: the schema becomes settings with ids, types and flags', () => {
  assert.equal(byId.context_size.type, 'int')
  assert.equal(byId.context_size.flag, '--ctx-size')
  assert.equal(byId['options.ubatch'].option, 'ubatch')
  assert.equal(byId['options.ubatch'].flag, '-ub')
  assert.equal(byId.flash_attention.type, 'enum')
  assert.deepEqual(byId.flash_attention.enum, ['on', 'off', 'auto'])
  assert.equal(byId['parameters.temperature'].type, 'float')
  assert.equal(byId['parameters.temperature'].key, 'parameters.temperature')
  assert.equal(byId['options.use_jinja'].group, 'template')
  assert.equal(byId.threads.group, 'speed')
  assert.ok(byId.mmlock)
  assert.ok(byId['options.-ot'])
})

test('llama.cpp runtime: the list form of the contract and messy entries still read', () => {
  const got = normalizeSchema([{ key: 'threads', type: 'integer', group: 'Memory & speed' }, { key: 'mlock', type: 'bool' }, { type: 'int' }, null])
  assert.equal(got.length, 2)
  assert.equal(got[0].type, 'int')
  assert.equal(got[0].flag, '--threads')
  // mlock is spelled mmlock in a model file.
  assert.equal(got[1].key, 'mmlock')
  assert.deepEqual(normalizeSchema(null), [])
})

test('llama.cpp runtime: groups come in order and composite settings are left out', () => {
  const groups = groupItems(items)
  assert.deepEqual(groups.map(g => g.id), ['memory', 'speed', 'sampling', 'template'])
  const memory = groups[0].items.map(i => i.id)
  assert.ok(memory.includes('context_size'))
  assert.ok(!memory.includes('mmap'))
  assert.ok(!memory.includes('options.direct_io'))
  assert.ok(!groups.flatMap(g => g.items).some(i => i.id === 'options.-ot'))
})

test('llama.cpp runtime: options are read and written by name, keeping other entries', () => {
  const opts = ['a:1', 'n_ubatch:32', 'b']
  assert.equal(readOption(opts, 'ubatch'), '32')
  assert.equal(readOption(opts, 'b'), null)
  assert.equal(readOption(opts, 'c'), undefined)
  assert.deepEqual(writeOption(opts, 'ubatch', '64'), ['a:1', 'ubatch:64', 'b'])
  assert.deepEqual(writeOption(opts, 'ubatch', undefined), ['a:1', 'b'])
  assert.deepEqual(writeOption(['n_ubatch:1', 'ubatch:2'], 'ubatch', '3'), ['ubatch:3'])
  assert.equal(writeOption(['ubatch:1'], 'ubatch', undefined), undefined)
  assert.deepEqual(writeEntry(['override_tensor:a=CPU', 'x'], '-ot', '-ot:b=CPU'), ['-ot:b=CPU', 'x'])
  assert.equal(readEntry(['x', '-ot:b=CPU'], 'override_tensor'), '-ot:b=CPU')
})

test('llama.cpp runtime: a setting reads and writes its key or its option', () => {
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

test('llama.cpp runtime: typed text is validated against type and bounds', () => {
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

test('llama.cpp runtime: the load mode folds mmap, mlock and direct I/O', () => {
  assert.equal(readLoadMode({}), 'default')
  for (const mode of ['none', 'mmap', 'mlock', 'mmap_mlock', 'direct_io', 'default']) {
    const draft = { options: ['x:1'] }
    writeLoadMode(draft, mode)
    assert.equal(readLoadMode(draft), mode, mode)
  }
  // As the server's parser writes --load-mode none.
  const none = { mmlock: true }
  writeLoadMode(none, 'none')
  assert.deepEqual(none, { mmap: false })
  const direct = { mmap: false }
  writeLoadMode(direct, 'direct_io')
  assert.deepEqual(direct, { options: ['direct_io:true'] })
})

test('llama.cpp runtime: tensor placement choices become server requests', () => {
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
})

test('llama.cpp runtime: a placement the server read back becomes a choice', () => {
  const { placements } = INKLING
  assert.deepEqual(choiceFromPlacement(placements[0].placement), { mode: 'ranges', ranges: [{ from: 0, to: 6 }, { from: 18, to: 23 }] })
  assert.deepEqual(choiceFromPlacement({ mode: 'experts_cpu_all' }), { mode: 'experts' })
  assert.deepEqual(choiceFromPlacement({ mode: 'first_layers_gpu', first_gpu_layers: 4 }), { mode: 'rest', gpuLayers: 4, totalLayers: '' })
  assert.deepEqual(choiceFromPlacement(null, 'token_embd=CPU'), { mode: 'raw', raw: 'token_embd=CPU' })
  assert.deepEqual(choiceFromPlacement(null, undefined), { mode: 'off' })
  assert.equal(patternOf('-ot:blk\\.1\\.x=CPU'), 'blk\\.1\\.x=CPU')
  assert.equal(patternOf('override_tensor:a=CPU'), 'a=CPU')
})

test('llama.cpp runtime: an -ot value typed by hand is checked', () => {
  assert.equal(validateOverride(''), null)
  assert.equal(validateOverride('blk\\.[0-9]+\\.ffn_.*_exps.*=CPU,token_embd=CUDA0'), null)
  assert.equal(validateOverride('no-target').error, 'overrideShape')
  assert.equal(validateOverride('blk\\.(=CPU').error, 'overrideRegex')
  assert.equal(validateOverride('x=C P U').error, 'overrideTarget')
})

test('llama.cpp runtime: raw options leave out what the form owns', () => {
  const owned = ownedOptionNames(items)
  const opts = ['parallel:1', 'cpu_moe:true', '-ot:x=CPU', 'override_tensor:y=CPU', 'context_shift:true', '--foo:bar']
  assert.deepEqual(rawOptions(opts, owned), ['context_shift:true', '--foo:bar'])
  assert.deepEqual(replaceRawOptions(opts, owned, ['context_shift:false']),
    ['parallel:1', 'cpu_moe:true', '-ot:x=CPU', 'override_tensor:y=CPU', 'context_shift:false'])
  assert.deepEqual(replaceRawOptions(['a:1'], owned, ['a:1', 'b:2']), ['a:1', 'b:2'])
  assert.equal(replaceRawOptions(['a:1'], owned, []), undefined)
  assert.equal(validateRawOption('  ', owned).error, 'empty')
  assert.equal(validateRawOption('n_parallel:2', owned).error, 'owned')
  assert.equal(validateRawOption('-ot:a=CPU', owned).error, 'owned')
  assert.equal(validateRawOption('context_shift:true', owned), null)
  assert.equal(rawOptionFor({ flag: '--rope-scale', value: '2' }), '--rope-scale:2')
  assert.equal(rawOptionFor({ flag: 'no-warmup', value: '' }), '--no-warmup')
})

test('llama.cpp runtime: a preset fills settings, lists them and undoes them', () => {
  const before = { context_size: 8192, options: ['context_shift:true'] }
  const draft = structuredClone(before)
  applyPreset(draft, items, SETUP_PRESETS.find(p => p.id === 'bigMoe'), '-ot:\\.ffn_.*_exps.*=CPU')
  assert.equal(draft.flash_attention, 'on')
  assert.equal(readEntry(draft.options, '-ot'), '-ot:\\.ffn_.*_exps.*=CPU')
  assert.ok(draft.options.includes('context_shift:true'))
  const changes = describeChanges(items, before, draft)
  const ids = changes.map(c => c.id)
  assert.ok(ids.includes('flash_attention'))
  assert.ok(ids.includes('options.-ot'))
  assert.ok(!ids.includes('context_size'))
  // An edit after the preset survives its undo.
  draft.cache_type_k = 'f16'
  undoChanges(draft, changes)
  assert.equal(draft.cache_type_k, 'f16')
  assert.equal(draft.flash_attention, undefined)
  assert.deepEqual(draft.options, ['context_shift:true'])
  // A preset with settings the schema lacks writes only what it has.
  const bare = {}
  applyPreset(bare, [byId.context_size], SETUP_PRESETS.find(p => p.id === 'lowVram'))
  assert.deepEqual(bare, { context_size: 4096 })
})

test('llama.cpp runtime: the Inkling command fills every field', () => {
  const draft = { 'parameters.model': 'inkling.gguf', options: ['parallel:4', 'context_shift:true'] }
  const { modelFile } = applyParsed(draft, items, INKLING)
  assert.equal(modelFile, 'Inkling-2-Mini-UD-Q3_K_XL.gguf')
  assert.equal(draft['parameters.model'], 'inkling.gguf')
  const want = {
    context_size: 18000, threads: 6, gpu_layers: 99, flash_attention: 'on', cache_type_k: 'q8_0', cache_type_v: 'q8_0',
    'parameters.temperature': 1, 'parameters.top_p': 1, 'parameters.top_k': 0, 'parameters.min_p': 0,
    'options.use_jinja': true, 'options.parallel': 1, 'options.ubatch': 64, 'options.threads_batch': 12,
    'options.fit': 'off', 'options.no_op_offload': true,
  }
  for (const [id, v] of Object.entries(want)) assert.equal(readItem(draft, byId[id]), v, id)
  assert.equal(readLoadMode(draft), 'none')
  assert.equal(readEntry(draft.options, '-ot'), INKLING.placements[0].option)
  assert.equal(draft.options.filter(e => e.startsWith('parallel')).length, 1)
  assert.ok(draft.options.includes('context_shift:true'))
  assert.deepEqual(INKLING.unmapped, [])
  // Nothing the command set is left over as a raw option.
  assert.deepEqual(rawOptions(draft.options, ownedOptionNames(items)), ['context_shift:true'])
})

test('llama.cpp runtime: flags with no field are kept as the server wrote them', () => {
  const draft = {}
  applyParsed(draft, items, MIXED)
  assert.deepEqual(draft.options, ['--rope-freq-base:10000', '--no-warmup'])
  assert.equal(draft.context_size, 4096)
})

test('llama.cpp runtime: diffs and nesting for the editor and the preview', () => {
  assert.deepEqual(diffPaths({ a: 1, b: 2 }, { a: 1, c: 3 }), [['b', undefined], ['c', 3]])
  assert.deepEqual(unflatten({ 'parameters.top_k': 0, context_size: 1, gone: undefined }), { parameters: { top_k: 0 }, context_size: 1 })
})
