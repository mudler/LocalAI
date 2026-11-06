import test from 'node:test'
import assert from 'node:assert/strict'
import { REASONING_EFFORTS, SETTING_RANGES, settingsBody, settingsErrors, settingsForm, settingsWithEffort } from '../../src/utils/chatSettings.js'
import { sliderFill, sliderText, sliderValue } from '../../src/components/ui/sliderModel.js'

test('slider ranges match what the server accepts', () => {
  const { temperature, top_p: topP, top_k: topK } = SETTING_RANGES
  assert.deepEqual([temperature.min, temperature.max], [0, 2])
  assert.deepEqual([topP.min, topP.max, topP.exclusiveMin], [0, 1, true])
  assert.equal(topK.min, 0)
  assert.equal(topK.integer, true)
  // Every end of a slider is a value the form accepts, and the first step of
  // top_p (the server needs it above 0) is too.
  const base = settingsForm({})
  assert.deepEqual(settingsErrors({ ...base, temperature: '0' }), {})
  assert.deepEqual(settingsErrors({ ...base, temperature: '2' }), {})
  assert.deepEqual(settingsErrors({ ...base, top_p: String(topP.step) }), {})
  assert.deepEqual(settingsErrors({ ...base, top_p: '1' }), {})
  assert.deepEqual(settingsErrors({ ...base, top_k: String(topK.max) }), {})
  assert.equal(settingsErrors({ ...base, top_p: '0' }).top_p, 'topP')
})

test('slider value: empty is not set, others are clamped to the ends and snapped to the step', () => {
  const t = SETTING_RANGES.temperature
  assert.equal(sliderValue(t, ''), null)
  assert.equal(sliderValue(t, '  '), null)
  assert.equal(sliderValue(t, 'abc'), null)
  assert.equal(sliderValue(t, '0.7'), 0.7)
  assert.equal(sliderValue(t, '0.73'), 0.75)
  assert.equal(sliderValue(t, '-1'), 0)
  assert.equal(sliderValue(t, '9'), 2)
  const p = SETTING_RANGES.top_p
  assert.equal(sliderValue(p, '0'), 0.01)
  assert.equal(sliderValue(p, '0.9'), 0.9)
  assert.equal(sliderValue(p, '5'), 1)
  const k = SETTING_RANGES.top_k
  assert.equal(sliderValue(k, '40'), 40)
  assert.equal(sliderValue(k, '4000'), 200)
  assert.equal(sliderValue(k, '12.4'), 12)
})

test('slider text and fill: no floating point tails, and the fill follows the position', () => {
  const t = SETTING_RANGES.temperature
  assert.equal(sliderText(t, 0.1 + 0.2), '0.3')
  assert.equal(sliderText(t, '1.15'), '1.15')
  assert.equal(sliderText(SETTING_RANGES.top_k, '40'), '40')
  assert.equal(sliderText(t, 'x'), '')
  assert.equal(sliderFill(t, null), 0)
  assert.equal(sliderFill(t, 1), 50)
  assert.equal(sliderFill(t, 2), 100)
  assert.equal(sliderFill(SETTING_RANGES.top_p, 0.01), 0)
})

test('settings body: slider values reach the request as numbers, and reset ones are left out', () => {
  const f = { ...settingsForm({}), temperature: '0.65', top_p: '0.95', top_k: '40' }
  assert.deepEqual(settingsBody(f), { temperature: 0.65, top_p: 0.95, top_k: 40 })
  assert.deepEqual(settingsBody({ ...f, temperature: '', top_k: '' }), { top_p: 0.95 })
})

test('chip and sheet: a change of the effort keeps every other setting, so both write the same body', () => {
  const stored = { system_prompt: 'Be brief', temperature: 0.2, top_p: 0.9, top_k: 40, context_size: 8192, reasoning_effort: 'low' }
  assert.deepEqual(settingsWithEffort(stored, 'high'), { ...stored, reasoning_effort: 'high' })
  // Default returns the choice to the model: the field is left out.
  const { reasoning_effort: _gone, ...rest } = stored
  assert.deepEqual(settingsWithEffort(stored, ''), rest)
  assert.deepEqual(settingsWithEffort(null, 'none'), { reasoning_effort: 'none' })
  // What the chip reads back is what the form shows.
  for (const v of REASONING_EFFORTS) assert.equal(settingsForm(settingsWithEffort(stored, v)).reasoning_effort, v)
  assert.equal(settingsErrors(settingsForm(settingsWithEffort(stored, 'none'))).reasoning_effort, undefined)
})
