import { test, expect } from './coverage-fixtures.js'

// 3D generation page: mock the capabilities + generation endpoints, feed a
// real (tiny) Form-B GLB through the parser/viewer, and exercise the
// IndexedDB-backed history. All assertions are DOM/text — never pixels — so
// the suite passes with or without working WebGL2 in headless Chromium.

function mockCapabilities(page) {
  return page.route('**/api/models/capabilities', (route) => {
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: [{ id: 'trellis-test-model', capabilities: ['FLAG_3D'] }] }),
    })
  })
}

// A valid one-triangle GLB in trellis2cpp's vertex-PBR form: POSITION/NORMAL
// f32, COLOR_0 u16-normalized VEC4, _METALLIC_ROUGHNESS u8-normalized VEC2,
// u32 indices — the layout mesh_export.cpp's write_vertex_glb emits.
function buildTinyGlb() {
  const positions = new Float32Array([0, 0, 0, 1, 0, 0, 0, 1, 0])
  const normals = new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1])
  const colors = new Uint16Array([
    65535, 0, 0, 65535,
    0, 65535, 0, 65535,
    0, 0, 65535, 65535,
  ])
  const metalRough = new Uint8Array([0, 153, 0, 153, 0, 153])
  const indices = new Uint32Array([0, 1, 2])

  const views = []
  let binLength = 0
  const addView = (typed) => {
    const byteOffset = binLength
    views.push({ buffer: 0, byteOffset, byteLength: typed.byteLength, target: 34962 })
    binLength += typed.byteLength
    binLength += (4 - (binLength % 4)) % 4
    return views.length - 1
  }
  addView(positions); addView(normals); addView(colors); addView(metalRough)
  const idxView = addView(indices)
  views[idxView].target = 34963

  const json = {
    asset: { version: '2.0', generator: 'threed-gen.spec' },
    scene: 0,
    scenes: [{ nodes: [0] }],
    nodes: [{ mesh: 0 }],
    meshes: [{ primitives: [{ attributes: { POSITION: 0, NORMAL: 1, COLOR_0: 2, _METALLIC_ROUGHNESS: 3 }, indices: 4, material: 0 }] }],
    materials: [{ pbrMetallicRoughness: { baseColorFactor: [1, 1, 1, 1], metallicFactor: 0, roughnessFactor: 0.6 }, doubleSided: true }],
    accessors: [
      { bufferView: 0, componentType: 5126, count: 3, type: 'VEC3', min: [0, 0, 0], max: [1, 1, 0] },
      { bufferView: 1, componentType: 5126, count: 3, type: 'VEC3' },
      { bufferView: 2, componentType: 5123, normalized: true, count: 3, type: 'VEC4' },
      { bufferView: 3, componentType: 5121, normalized: true, count: 3, type: 'VEC2' },
      { bufferView: 4, componentType: 5125, count: 3, type: 'SCALAR' },
    ],
    bufferViews: views,
    buffers: [{ byteLength: binLength }],
  }

  const bin = Buffer.alloc(binLength)
  const parts = [positions, normals, colors, metalRough, indices]
  for (let i = 0; i < parts.length; i++) {
    Buffer.from(parts[i].buffer, parts[i].byteOffset, parts[i].byteLength).copy(bin, views[i].byteOffset)
  }

  let jsonText = JSON.stringify(json)
  while (jsonText.length % 4 !== 0) jsonText += ' '
  const jsonBuf = Buffer.from(jsonText)

  const total = 12 + 8 + jsonBuf.length + 8 + bin.length
  const glb = Buffer.alloc(total)
  glb.writeUInt32LE(0x46546c67, 0)   // magic 'glTF'
  glb.writeUInt32LE(2, 4)
  glb.writeUInt32LE(total, 8)
  glb.writeUInt32LE(jsonBuf.length, 12)
  glb.writeUInt32LE(0x4e4f534a, 16)  // 'JSON'
  jsonBuf.copy(glb, 20)
  const binHeader = 20 + jsonBuf.length
  glb.writeUInt32LE(bin.length, binHeader)
  glb.writeUInt32LE(0x004e4942, binHeader + 4) // 'BIN\0'
  bin.copy(glb, binHeader + 8)
  return glb
}

// 1x1 transparent PNG for the conditioning-image upload.
const TINY_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
  'base64',
)

function mockGeneration(page, onRequest) {
  return page.route('**/v1/3d/generations', (route) => {
    if (route.request().method() !== 'POST') return route.continue()
    onRequest?.(route.request().postDataJSON())
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        created: Math.floor(Date.now() / 1000),
        data: [{ url: '/generated-3d/test.glb' }],
      }),
    })
  })
}

function mockGlbDownload(page) {
  return page.route('**/generated-3d/test.glb', (route) => {
    route.fulfill({
      status: 200,
      headers: { 'Content-Type': 'model/gltf-binary' },
      body: buildTinyGlb(),
    })
  })
}

async function generateOnce(page) {
  await page.goto('/app/3d')
  await expect(page.getByRole('button', { name: 'trellis-test-model' })).toBeVisible({ timeout: 10_000 })
  await page.locator('#threed-image-file').setInputFiles({
    name: 'input.png',
    mimeType: 'image/png',
    buffer: TINY_PNG,
  })
  await page.locator('button[type="submit"]').click()
}

test.describe('3D generation', () => {
  test.beforeEach(async ({ page }) => {
    await mockCapabilities(page)
    await mockGlbDownload(page)
  })

  test('generates, shows mesh stats, and offers a GLB download', async ({ page }) => {
    let requestBody = null
    await mockGeneration(page, (body) => { requestBody = body })

    await generateOnce(page)

    // Stats render from the parsed GLB even without working GL.
    await expect(page.getByTestId('glb-stats')).toContainText('3', { timeout: 15_000 })
    await expect(page.getByTestId('glb-stats')).toContainText('1')
    await expect(page.getByTestId('glb-stats')).toContainText('PBR')

    const download = page.getByTestId('glb-download')
    await expect(download).toBeVisible()
    await expect(download).toHaveAttribute('href', /^blob:/)
    await expect(download).toHaveAttribute('download', /\.glb$/)

    expect(requestBody.model).toBe('trellis-test-model')
    expect(requestBody.image).toBeTruthy()
    expect(requestBody.quality).toBe('auto')
    expect(requestBody.background).toBe('auto')
    expect(requestBody.response_format).toBe('url')
  })

  test('advanced settings map to step/texture_steps/cfg_scale/seed', async ({ page }) => {
    let requestBody = null
    await mockGeneration(page, (body) => { requestBody = body })

    await page.goto('/app/3d')
    await expect(page.getByRole('button', { name: 'trellis-test-model' })).toBeVisible({ timeout: 10_000 })
    await page.locator('#threed-image-file').setInputFiles({ name: 'input.png', mimeType: 'image/png', buffer: TINY_PNG })

    await page.locator('select').first().selectOption('512')
    await page.getByRole('button', { name: /Advanced Settings/ }).click()
    const advanced = page.locator('#threed-advanced-options')
    await advanced.locator('input').nth(0).fill('20')   // steps
    await advanced.locator('input').nth(1).fill('8')    // texture steps
    await advanced.locator('input').nth(2).fill('5.5')  // guidance
    await advanced.locator('input').nth(3).fill('42')   // seed
    await page.locator('button[type="submit"]').click()

    await expect(page.getByTestId('glb-download')).toBeVisible({ timeout: 15_000 })
    expect(requestBody.quality).toBe('512')
    expect(requestBody.step).toBe(20)
    expect(requestBody.texture_steps).toBe(8)
    expect(requestBody.cfg_scale).toBe(5.5)
    expect(requestBody.seed).toBe(42)
  })

  test('history entry persists across navigation and reloads into the viewer', async ({ page }) => {
    await mockGeneration(page)
    await generateOnce(page)
    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 15_000 })

    // IndexedDB persists within the browser context — navigate away and back.
    await page.goto('/app')
    await page.goto('/app/3d')
    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 15_000 })

    // Selecting the entry loads the stored Blob back into the viewer.
    await page.getByTestId('media-history-item').click()
    await expect(page.getByTestId('glb-stats')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('glb-download')).toHaveAttribute('href', /^blob:/)
  })

  test('deleting a history entry removes it', async ({ page }) => {
    await mockGeneration(page)
    await generateOnce(page)
    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 15_000 })

    await page.getByTestId('media-history-delete').click()
    await expect(page.getByTestId('media-history-item')).toHaveCount(0)
  })

  test('API errors surface through the trace link error box', async ({ page }) => {
    await page.route('**/v1/3d/generations', (route) => {
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: { message: 'trellis2 pipeline load: missing files' } }),
      })
    })

    await generateOnce(page)
    await expect(page.locator('.media-result')).toContainText(/missing files|error/i, { timeout: 15_000 })
  })
})
