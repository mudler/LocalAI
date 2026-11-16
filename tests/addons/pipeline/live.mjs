// Explicit live-backend contract smoke. Creates one image and one GLB; no model
// installs, forced unloads, server replacement or configuration changes.
import { mkdtemp, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const base = process.env.LOCALAI_URL || 'http://127.0.0.1:8081'
const headers = { 'Content-Type': 'application/json' }
if (process.env.LOCALAI_API_KEY) headers.Authorization = `Bearer ${process.env.LOCALAI_API_KEY}`
const dir = await mkdtemp(join(tmpdir(), 'pipeline-live-'))
async function post(path, body) {
  const start = Date.now()
  const response = await fetch(base + path, { method: 'POST', headers, body: JSON.stringify(body), signal: AbortSignal.timeout(15 * 60_000) })
  if (!response.ok) throw new Error(`${path}: ${response.status} ${(await response.text()).slice(0, 1000)}`)
  const result = await response.json()
  console.log(`${path}: ${Math.round((Date.now() - start)/1000)} seconds`)
  return result
}
async function bytes(result) {
  const value = result.data?.[0]
  if (value?.b64_json) return Buffer.from(value.b64_json, 'base64')
  if (!value?.url) throw new Error('Backend did not return an asset')
  const url = new URL(value.url, base)
  if (url.origin !== new URL(base).origin) throw new Error('Refusing foreign asset URL')
  const response = await fetch(url, { headers, signal: AbortSignal.timeout(60_000) })
  if (!response.ok) throw new Error(`Asset download: ${response.status}`)
  return Buffer.from(await response.arrayBuffer())
}
const image = await post('/v1/images/generations', {
  model: process.env.PIPELINE_IMAGE_MODEL || 'Z-Image-Turbo',
  prompt: 'A single low-poly wooden treasure chest, isolated game asset, three-quarter view, centered, solid pure black background, no ground, no text',
  size: '512x512', n: 1, step: 9, seed: 42, response_format: 'b64_json',
})
const png = await bytes(image)
await writeFile(join(dir, 'input.png'), png)
const mesh = await post('/3d/generations', {
  model: process.env.PIPELINE_3D_MODEL || 'trellis2-4b', image: `data:image/png;base64,${png.toString('base64')}`,
  quality: 'coarse', background: 'auto', step: 4, texture_steps: 4, seed: 42, response_format: 'b64_json',
})
const glb = await bytes(mesh)
if (glb.toString('ascii', 0, 4) !== 'glTF' || glb.readUInt32LE(4) !== 2 || glb.readUInt32LE(8) !== glb.length) throw new Error('Invalid GLB header')
await writeFile(join(dir, 'asset.glb'), glb)
console.log(JSON.stringify({ directory: dir, imageBytes: png.length, glbBytes: glb.length }))
