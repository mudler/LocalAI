import { test, expect } from './coverage-fixtures.js'

const animationOperation = {
  id: 'animate', endpoint: '/3d/animate', output: 'skeleton_animation',
  inputs: [{ name: 'prompt', type: 'text', label: 'Motion prompt', required: true, max_bytes: 4096 }],
  parameters: [
    { name: 'frames', label: 'Frames (30 FPS)', type: 'integer', default: '150', min: 60, max: 150 },
    { name: 'steps', label: 'Sampling steps', type: 'integer', default: '100', min: 1, max: 1000, advanced: true },
    { name: 'seed', label: 'Seed', type: 'uint64', default: '0', advanced: true },
  ],
}

function animationGlb(change = () => {}) {
  const values = new Float32Array([0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0, 0.70710678, 0, 0.70710678])
  const document = {
    asset: { version: '2.0' }, scene: 0, scenes: [{ nodes: [0] }],
    nodes: [{ name: 'root', children: [1] }, { name: 'joint', translation: [0, 1, 0] }],
    buffers: [{ byteLength: values.byteLength }],
    bufferViews: [{ buffer: 0, byteOffset: 0, byteLength: 8 }, { buffer: 0, byteOffset: 8, byteLength: 24 }, { buffer: 0, byteOffset: 32, byteLength: 32 }],
    accessors: [{ bufferView: 0, componentType: 5126, count: 2, type: 'SCALAR' }, { bufferView: 1, componentType: 5126, count: 2, type: 'VEC3' }, { bufferView: 2, componentType: 5126, count: 2, type: 'VEC4' }],
    animations: [{ samplers: [{ input: 0, output: 1 }, { input: 0, output: 2 }], channels: [{ sampler: 0, target: { node: 0, path: 'translation' } }, { sampler: 1, target: { node: 0, path: 'rotation' } }] }],
  }
  change(document)
  let json = JSON.stringify(document)
  json += ' '.repeat((4-json.length%4)%4)
  const output = Buffer.alloc(28+json.length+values.byteLength)
  output.writeUInt32LE(0x46546c67, 0); output.writeUInt32LE(2, 4); output.writeUInt32LE(output.length, 8)
  output.writeUInt32LE(json.length, 12); output.writeUInt32LE(0x4e4f534a, 16); output.write(json, 20)
  output.writeUInt32LE(values.byteLength, 20+json.length); output.writeUInt32LE(0x004e4942, 24+json.length)
  Buffer.from(values.buffer).copy(output, 28+json.length)
  return output
}

test.beforeEach(async ({ page }) => {
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: [
    { id: 'trellis-test', capabilities: ['FLAG_3D'] },
    { id: 'kimodo-test', capabilities: ['FLAG_3D_ANIMATION'], three_d_operations: [animationOperation] },
  ] } }))
  await page.route('**/generated-3d/animation.glb', route => route.fulfill({ contentType: 'model/gltf-binary', body: animationGlb() }))
})

test('switches inputs, generates animation, scrubs playback and restores history', async ({ page }) => {
  let request
  await page.route('**/3d/animate', route => {
    request = route.request().postDataJSON()
    return route.fulfill({ json: { data: [{ url: '/generated-3d/animation.glb' }] } })
  })
  await page.goto('/app/studio/threed')
  await page.getByRole('button', { name: 'trellis-test', exact: true }).click()
  await page.getByRole('option', { name: 'kimodo-test', exact: true }).click()
  await expect(page.locator('#threed-image-file')).toHaveCount(0)
  await page.getByLabel('Motion prompt').fill('Walk forward and wave')
  await page.getByLabel('Frames (30 FPS)').fill('90')
  await page.getByRole('button', { name: 'Advanced', exact: false }).click()
  await page.getByLabel('Seed', { exact: true }).fill('18446744073709551615')
  await page.locator('button[type="submit"]').click()
  await expect(page.getByTestId('animation-viewer')).toBeVisible()
  await expect(page.getByTestId('animation-time')).toBeVisible()
  expect(request.inputs).toEqual({ prompt: { type: 'text', data: 'Walk forward and wave' } })
  expect(request.params).toEqual({ frames: '90', seed: '18446744073709551615' })
  expect(request).not.toHaveProperty('image')
  expect(request).not.toHaveProperty('quality')
  await expect(page.getByTestId('glb-remesh')).toHaveCount(0)
  await page.getByRole('button', { name: 'Pause', exact: true }).click()
  await page.getByLabel('Timeline', { exact: true }).fill('0.5')
  await expect(page.getByTestId('animation-time')).toContainText('0.50')
  await page.getByLabel('Zoom', { exact: true }).fill('1.5')
  await page.getByRole('button', { name: 'Reset view', exact: true }).click()
  await expect(page.getByLabel('Zoom', { exact: true })).toHaveValue('1')
  await expect(page.getByTestId('glb-download')).toBeVisible()
  await expect(page.getByTestId('media-history-item')).toContainText('Walk forward and wave')
  await page.getByLabel('Motion prompt').fill('A different prompt')
  await page.getByTestId('media-history-item').click()
  await expect(page.getByLabel('Motion prompt')).toHaveValue('Walk forward and wave')
  await page.reload()
  await page.getByTestId('media-history-item').click()
  await expect(page.getByTestId('animation-viewer')).toBeVisible()
  await expect(page.getByLabel('Motion prompt')).toHaveValue('Walk forward and wave')
  await page.getByRole('button', { name: 'kimodo-test', exact: true }).click()
  await page.getByRole('option', { name: /^trellis-test/ }).click()
  await expect(page.locator('#threed-image-file')).toBeAttached()
  await expect(page.getByLabel('Motion prompt')).toHaveCount(0)
})

test('plays in real time across delayed frames, looping, pause and seek', async ({ page }) => {
  await page.route('**/3d/animate', route => route.fulfill({ json: { data: [{ url: '/generated-3d/animation.glb' }] } }))
  await page.goto('/app/studio/threed')
  await page.getByRole('button', { name: 'trellis-test', exact: true }).click()
  await page.getByRole('option', { name: 'kimodo-test', exact: true }).click()
  await page.getByLabel('Motion prompt').fill('Walk.')
  await page.getByRole('button', { name: /Generate$/ }).click()
  await page.getByRole('button', { name: 'Pause', exact: true }).click()
  await page.getByLabel('Timeline', { exact: true }).fill('0')

  // Drive RAF timestamps independently of rendering so dropped frames and
  // deferred React state updates cannot silently slow the playback clock.
  await page.evaluate(() => {
    let next = 0
    const callbacks = new Map()
    window.requestAnimationFrame = callback => { callbacks.set(++next, callback); return next }
    window.cancelAnimationFrame = id => callbacks.delete(id)
    window.advanceAnimationFrame = time => {
      const pending = [...callbacks.values()]
      callbacks.clear()
      for (const callback of pending) callback(time)
    }
  })
  const advance = time => page.evaluate(time => window.advanceAnimationFrame(time), time)
  const toggle = name => page.getByRole('button', { name, exact: true }).evaluate(button => button.click())
  const clock = page.getByTestId('animation-time')
  await toggle('Play')
  await expect(page.getByRole('button', { name: 'Pause', exact: true })).toBeVisible()
  await advance(0)
  await advance(250)
  await expect(clock).toHaveText('0.25 / 1.00 s')
  await advance(500)
  await expect(clock).toHaveText('0.50 / 1.00 s')
  await advance(1250)
  await expect(clock).toHaveText('0.25 / 1.00 s')
  await toggle('Pause')
  await expect(page.getByRole('button', { name: 'Play', exact: true })).toBeVisible()
  await advance(5000)
  await expect(clock).toHaveText('0.25 / 1.00 s')
  await page.getByLabel('Timeline', { exact: true }).fill('0.5')
  await toggle('Play')
  await expect(page.getByRole('button', { name: 'Pause', exact: true })).toBeVisible()
  await advance(6000)
  await advance(6250)
  await expect(clock).toHaveText('0.75 / 1.00 s')
})

test('keeps the 3D Studio category available with only an animation model', async ({ page }) => {
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: [
    { id: 'kimodo-test', capabilities: ['FLAG_3D_ANIMATION'], three_d_operations: [animationOperation] },
  ] } }))
  await page.goto('/app/studio/threed')
  await expect(page.getByLabel('Motion prompt')).toBeVisible()
  await expect(page.getByRole('button', { name: 'kimodo-test', exact: true })).toBeVisible()
})

test('validates the prompt byte limit before submitting', async ({ page }) => {
  await page.goto('/app/studio/threed?model=kimodo-test')
  await page.getByRole('button', { name: 'trellis-test', exact: true }).click()
  await page.getByRole('option', { name: 'kimodo-test', exact: true }).click()
  await page.getByLabel('Motion prompt').fill('🦊'.repeat(1025))
  await expect(page.getByRole('alert')).toContainText('4096 byte limit')
  expect(await page.getByLabel('Motion prompt').evaluate(element => element.checkValidity())).toBe(false)
  await page.getByLabel('Motion prompt').fill('Walk slowly.')
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('renders media conditioning and enumerated parameters from model capabilities', async ({ page }) => {
  const operation = { ...animationOperation, output: 'mesh', inputs: [
    animationOperation.inputs[0], { name: 'mesh', type: 'mesh', label: 'Source mesh', required: true },
  ], parameters: [{ name: 'mode', label: 'Motion mode', type: 'enum', default: 'loop', options: ['loop', 'once'] }] }
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: [
    { id: 'future-model', capabilities: ['FLAG_3D_ANIMATION'], three_d_operations: [operation] },
  ] } }))
  let request
  await page.route('**/3d/animate', route => {
    request = route.request().postDataJSON()
    return route.fulfill({ status: 400, json: { error: { message: 'Fixture stops after request validation' } } })
  })
  await page.goto('/app/studio/threed')
  await page.getByLabel('Motion prompt').fill('Wave.')
  await page.getByLabel('Source mesh').setInputFiles({ name: 'character.glb', mimeType: 'model/gltf-binary', buffer: animationGlb() })
  await page.getByLabel('Motion mode').selectOption('once')
  await page.getByRole('button', { name: /Generate$/ }).click()
  await expect.poll(() => request?.params.mode).toBe('once')
  expect(request.inputs.mesh.type).toBe('mesh')
  expect(Buffer.from(request.inputs.mesh.data, 'base64').subarray(0, 4).toString()).toBe('glTF')
  await expect(page.getByText('Generates an animated skeleton, without a mesh or skin.')).toHaveCount(0)
})

test('reports unsupported skeleton transforms without displaying a stale pose', async ({ page }) => {
  await page.route('**/generated-3d/animation.glb', route => route.fulfill({ contentType: 'model/gltf-binary', body: animationGlb(document => { document.nodes[1].translation = [0, 1] }) }))
  await page.route('**/3d/animate', route => route.fulfill({ json: { data: [{ url: '/generated-3d/animation.glb' }] } }))
  await page.goto('/app/studio/threed')
  await page.getByRole('button', { name: 'trellis-test', exact: true }).click()
  await page.getByRole('option', { name: 'kimodo-test', exact: true }).click()
  await page.getByLabel('Motion prompt').fill('Walk.')
  await page.getByRole('button', { name: /Generate$/ }).click()
  await expect(page.getByRole('alert')).toContainText('Invalid skeleton transform')
  await expect(page.getByLabel('Timeline', { exact: true })).toHaveCount(0)
})
