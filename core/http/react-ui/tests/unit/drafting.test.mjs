import test from 'node:test'
import assert from 'node:assert/strict'
import {
  normalizeSchema, groupItems, ownedOptionNames, rawOptions, describeChanges, readItem,
} from '../../src/utils/engineSettings.js'
import {
  CUSTOM_MODE, draftKinds, readDraftMode, writeDraftMode, offeredModes, modeAvailable, draftCandidates,
  nameFamily, draftFit, modelFacts, draftEstimateBody, draftMemory, VOCAB_TOLERANCE,
} from '../../src/utils/drafting.js'
// The answer of the real handler (pkg/enginesettings), saved for the specs.
import SCHEMA from '../../e2e/fixtures/engine-llamacpp-schema.json' with { type: 'json' }

const schema = normalizeSchema(SCHEMA)
const d = schema.drafting
const MTP = { mtpLayers: 1 }
const apply = (values, mode, opts) => {
  const draft = { ...values }
  writeDraftMode(schema, draft, mode, opts)
  return draft
}

test('drafting: the schema names the kind, the model, the streams option and four modes', () => {
  assert.ok(d)
  assert.equal(d.group, 'speculative')
  assert.equal(d.typeItem.id, 'options.spec_type')
  assert.equal(d.modelItem.id, 'draft_model')
  assert.equal(d.offType, 'none')
  assert.deepEqual(d.modes.map(m => m.id), ['off', 'ngram', 'model', 'mtp'])
  assert.deepEqual(d.modes[3].set, { 'options.spec_n_max': 6, 'options.spec_p_min': 0.75 })
  assert.deepEqual(d.modes[2].fields, ['options.spec_n_min', 'options.spec_n_max', 'options.spec_p_min'])
  // spec_max_streams is not in the table of this build: it becomes a
  // setting of its own, so it reads, writes and lists like any other.
  assert.equal(d.streams.item.id, 'options.spec_max_streams')
  assert.equal(d.streams.item.type, 'int')
  assert.ok(schema.items.includes(d.streams.item))
})

test('drafting: the streams option keeps the table entry when the engine lists one', () => {
  const raw = structuredClone(SCHEMA)
  raw.flags = raw.flags.filter(f => f.option !== 'spec_max_streams')
  raw.flags.push({ key: 'spec_max_streams', target: 'option', path: 'options', option: 'spec_max_streams', type: 'int', min: 0, default: '4', doc: 'from the table', group: 'speculative', localai_only: true })
  const s = normalizeSchema(raw)
  assert.equal(s.items.filter(i => i.option === 'spec_max_streams').length, 1)
  assert.equal(s.drafting.streams.item.doc, 'from the table')
  assert.equal(s.drafting.streams.item.default, '4')
})

test('drafting: the drafting group is the Drafting block, with what no mode draws as more options', () => {
  const block = groupItems(schema).find(g => g.id === 'speculative')
  assert.equal(block.drafting, true)
  const ids = block.items.map(i => i.id)
  for (const owned of ['options.spec_type', 'draft_model', 'options.spec_max_streams', 'options.spec_n_max', 'options.spec_ngram_mod_n_min']) {
    assert.ok(!ids.includes(owned), owned)
  }
  assert.ok(ids.includes('options.draft_gpu_layers'))
  assert.ok(ids.includes('options.spec_ngram_size_n'))
  // No other block draws the kind, the model or the streams option.
  for (const g of groupItems(schema)) {
    if (g.drafting) continue
    for (const i of g.items) assert.ok(!i.drafting, i.id)
  }
})

test('drafting: the streams option is not a raw option', () => {
  const owned = ownedOptionNames(schema)
  assert.ok(owned.has('spec_max_streams'))
  assert.deepEqual(rawOptions(['spec_max_streams:2', 'keep:1'], owned), ['keep:1'])
})

test('drafting: kinds are read as the backend reads them', () => {
  assert.deepEqual(draftKinds(undefined), [])
  assert.deepEqual(draftKinds('ngram_mod'), ['ngram-mod'])
  assert.deepEqual(draftKinds('draft, eagle3'), ['draft-simple', 'draft-eagle3'])
  assert.deepEqual(draftKinds('NGRAM-SIMPLE,,'), ['ngram-simple'])
})

test('drafting: the mode is read from the kind, the draft model and the model facts', () => {
  const read = (values, facts) => readDraftMode(schema, values, facts).mode
  assert.equal(read({}), 'off')
  assert.equal(read({ options: ['spec_type:none'] }), 'off')
  assert.equal(read({ options: ['spec_type:ngram-mod'] }), 'ngram')
  assert.equal(read({ options: ['speculative_type:ngram_simple'] }), 'ngram')
  assert.equal(read({ options: ['spec_type:draft-mtp'] }), 'mtp')
  assert.equal(read({ draft_model: 'small.gguf' }), 'model')
  assert.equal(read({ draft_model: 'small.gguf', options: ['spec_type:draft-eagle3'] }), 'model')
  // Two kinds no single mode reads back.
  assert.equal(read({ options: ['spec_type:ngram-mod,draft-mtp'] }), CUSTOM_MODE)
  assert.equal(read({ options: ['spec_type:draft-dflash'] }), CUSTOM_MODE)
  // A model with an MTP head drafts with it when the file names no kind.
  const auto = readDraftMode(schema, {}, MTP)
  assert.equal(auto.mode, 'mtp')
  assert.equal(auto.auto, true)
  assert.equal(read({ options: ['spec_type:none'] }, MTP), 'off')
})

test('drafting: MTP is offered only for a model with an MTP head, or when the file uses it', () => {
  assert.equal(modeAvailable(d.modes[3], {}), false)
  assert.equal(modeAvailable(d.modes[3], MTP), true)
  assert.deepEqual(offeredModes(schema, {}, 'off').map(m => m.id), ['off', 'ngram', 'model'])
  assert.deepEqual(offeredModes(schema, MTP, 'off').map(m => m.id), ['off', 'ngram', 'model', 'mtp'])
  assert.deepEqual(offeredModes(schema, {}, 'mtp').map(m => m.id), ['off', 'ngram', 'model', 'mtp'])
})

test('drafting: picking a mode writes the kind, the model and the mode values, and keeps the rest', () => {
  const base = { context_size: 8192, options: ['keep_me:true', 'draft_gpu_layers:10'] }
  const ngram = apply(base, 'ngram')
  assert.deepEqual(ngram.options, ['keep_me:true', 'draft_gpu_layers:10', 'spec_type:ngram-mod'])
  assert.equal(ngram.draft_model, undefined)

  const model = apply(ngram, 'model', { file: 'small.gguf' })
  assert.equal(model.draft_model, 'small.gguf')
  assert.deepEqual(model.options, ['keep_me:true', 'draft_gpu_layers:10'])

  // A draft-* kind the person chose stays with a draft model.
  const eagle = apply({ options: ['spec_type:draft-eagle3'] }, 'model', { file: 'eagle.gguf' })
  assert.deepEqual(eagle.options, ['spec_type:draft-eagle3'])

  const mtp = apply(model, 'mtp', { facts: MTP })
  assert.equal(mtp.draft_model, undefined)
  assert.deepEqual(mtp.options, ['keep_me:true', 'draft_gpu_layers:10', 'spec_type:draft-mtp', 'spec_n_max:6', 'spec_p_min:0.75'])
  // A value the person set is not replaced by the mode's value.
  const kept = apply({ options: ['spec_n_max:2'] }, 'mtp', { facts: MTP })
  assert.deepEqual(kept.options, ['spec_n_max:2', 'spec_type:draft-mtp', 'spec_p_min:0.75'])
})

test('drafting: Off removes the drafting setup and says none for a model that drafts by itself', () => {
  const on = { draft_model: 'small.gguf', options: ['keep_me:true', 'spec_n_max:8', 'spec_p_min:0.5', 'spec_max_streams:2', 'draft_gpu_layers:10'] }
  const off = apply(on, 'off')
  assert.equal(off.draft_model, undefined)
  assert.deepEqual(off.options, ['keep_me:true', 'draft_gpu_layers:10'])
  const offMtp = apply({ options: ['spec_type:draft-mtp'] }, 'off', { facts: MTP })
  assert.deepEqual(offMtp.options, ['spec_type:none'])
  assert.equal(readDraftMode(schema, offMtp, MTP).mode, 'off')
  // Off of an empty file stays empty.
  assert.deepEqual(apply({}, 'off'), {})
})

test('drafting: a mode change is listed and can be undone like any setting', () => {
  const before = { options: ['spec_type:ngram-mod'] }
  const after = apply(before, 'model', { file: 'small.gguf' })
  const ids = describeChanges(schema, before, after).map(c => c.id).sort()
  assert.deepEqual(ids, ['draft_model', 'options.spec_type'])
  const streams = describeChanges(schema, {}, { options: ['spec_max_streams:2'] })
  assert.deepEqual(streams.map(c => [c.id, c.to]), [['options.spec_max_streams', 2]])
  assert.equal(readItem({ options: ['spec_max_streams:0'] }, d.streams.item), 0)
})

test('drafting: candidates are local text models of the same engine, the same family first', () => {
  const models = [
    { id: 'qwen3-32b', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
    { id: 'qwen3-0.6b', backend: 'cuda12-llama-cpp', capabilities: ['FLAG_CHAT', 'FLAG_COMPLETION'] },
    { id: 'llama-3.2-1b', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'] },
    { id: 'whisper', backend: 'whisper', capabilities: ['FLAG_TRANSCRIPT'] },
    { id: 'embed', backend: 'llama-cpp', capabilities: ['FLAG_EMBEDDINGS'] },
    { id: 'cloud', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'], remote: true },
    { id: 'alias', backend: 'llama-cpp', capabilities: ['FLAG_CHAT'], alias_of: 'qwen3-0.6b' },
    { id: 'vllm-model', backend: 'vllm', capabilities: ['FLAG_CHAT'] },
  ]
  const served = (b) => b === 'llama-cpp' || b === 'cuda12-llama-cpp'
  assert.deepEqual(draftCandidates(models, 'qwen3-32b', served), [
    { id: 'qwen3-0.6b', backend: 'cuda12-llama-cpp', sameFamily: true },
    { id: 'llama-3.2-1b', backend: 'llama-cpp', sameFamily: false },
  ])
  assert.equal(nameFamily('Qwen3-32B-Q4'), 'qwen3')
  assert.equal(nameFamily('llama-3.2-1b'), 'llama')
  assert.equal(nameFamily(''), '')
})

test('drafting: the fit compares the vocabularies, then the families', () => {
  const qwen = { architecture: 'qwen3', vocabSize: 151936 }
  assert.deepEqual(draftFit(qwen, { architecture: 'qwen3', vocabSize: 151936 }), { kind: 'ok', vocab: 151936, sameArch: true, architecture: 'qwen3' })
  assert.equal(draftFit(qwen, { architecture: 'qwen3', vocabSize: 151936 + VOCAB_TOLERANCE }).kind, 'ok')
  assert.deepEqual(draftFit(qwen, { architecture: 'llama', vocabSize: 128256 }), { kind: 'mismatch', main: 151936, draft: 128256 })
  assert.deepEqual(draftFit({ architecture: 'gemma4' }, { architecture: 'gemma4-assistant' }), { kind: 'family', architecture: 'gemma4' })
  assert.deepEqual(draftFit({}, {}, { main: 'qwen3-32b', draft: 'qwen3-0.6b' }), { kind: 'family', architecture: 'qwen3' })
  assert.deepEqual(draftFit(null, null, { main: 'a', draft: 'b' }), { kind: 'unknown' })
})

test('drafting: facts, the draft estimate request and its memory words', () => {
  assert.equal(modelFacts(null), null)
  assert.deepEqual(modelFacts({ architecture: 'qwen3', mtp_layers: 1, vocab_size: 151936, context_length: 8192 }),
    { architecture: 'qwen3', mtpLayers: 1, vocabSize: 151936, contextLength: 8192 })
  assert.equal(draftEstimateBody(schema, {}, ''), null)
  assert.deepEqual(draftEstimateBody(schema, { context_size: 16384, options: ['draft_cache_type_k:q8_0'] }, 'qwen3-0.6b'),
    { model: 'qwen3-0.6b', context_size: 16384, cache_type_k: 'q8_0', cache_type_v: 'f16' })
  assert.equal(draftMemory({ message: 'no weight files found for estimation' }), null)
  const mem = draftMemory({ vram_bytes: 1610612736, size_bytes: 1073741824, context_length: 16384, runtime: { kv_cache_bytes: 536870912 } })
  assert.equal(mem.context, 16384)
  assert.equal(mem.approximate, false)
  assert.ok(mem.total && mem.weights && mem.kv)
})
