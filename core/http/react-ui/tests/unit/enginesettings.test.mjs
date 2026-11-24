import test from 'node:test'
import assert from 'node:assert/strict'
import {
  normalizeSchema, groupItems, readItem, writeItem, validate, readOption, writeOption, writeEntry,
  readEntry, readCombined, writeCombined, placementRequest, placementModes, choiceFromPlacement, patternOf,
  validateOverride, ownedOptionNames, rawOptions, replaceRawOptions, validateRawOption,
  rawOptionFor, applyPreset, applyParsed, describeChanges, undoChanges, diffPaths, unflatten,
  optionNames, hasCapability,
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
  assert.deepEqual(blocks.map(g => g.id), ['memory', 'speed', 'placement', 'sampling', 'template'])
  assert.equal(blocks[2].placement, true)
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

test('engine settings: a preset fills settings, lists them and undoes them', () => {
  const before = { context_size: 8192, options: ['context_shift:true'] }
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
  assert.ok(draft.options.includes('context_shift:true'))
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
  assert.deepEqual(draft.options, ['context_shift:true'])
  // A preset with settings the schema lacks writes only what it has.
  const bare = {}
  applyPreset({ ...schema, items: [byId.context_size] }, bare, preset('lowVram'))
  assert.deepEqual(bare, { context_size: 4096 })
})

test('engine settings: the Inkling command fills every field', () => {
  const draft = { 'parameters.model': 'inkling.gguf', options: ['parallel:4', 'context_shift:true'] }
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
  assert.ok(draft.options.includes('context_shift:true'))
  assert.deepEqual(INKLING.unmapped, [])
  // Nothing the command set is left over as a raw option.
  assert.deepEqual(rawOptions(draft.options, ownedOptionNames(schema)), ['context_shift:true'])
})

test('engine settings: flags with no field are kept as the server wrote them', () => {
  const draft = {}
  applyParsed(schema, draft, MIXED)
  assert.deepEqual(draft.options, ['--rope-freq-base:10000', '--no-warmup'])
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
