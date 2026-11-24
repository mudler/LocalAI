// SPDX-License-Identifier: MIT
import { CAP_UPSCALE } from './capabilities.js'

export function upscaleModels(models) {
  return models.filter(m => !m.disabled && m.capabilities?.includes(CAP_UPSCALE) &&
    Number.isFinite(m.upscaleScale) && m.upscaleScale > 0)
    .map(({ id, upscaleScale }) => ({ id, upscaleScale }))
}

export async function sourceImageFile(source, base = window.location.href, fetcher = fetch) {
  if (source instanceof File) return source
  const url = new URL(source, base)
  if (url.origin !== new URL(base).origin || !['http:', 'https:'].includes(url.protocol)) {
    throw new Error('Source image must be same-origin')
  }
  const response = await fetcher(url.href, { credentials: 'same-origin', mode: 'same-origin', redirect: 'error' })
  if (!response.ok) throw new Error(`Image fetch failed (HTTP ${response.status})`)
  if (!response.headers.get('content-type')?.toLowerCase().startsWith('image/')) throw new Error('Source is not an image')
  const blob = await response.blob()
  return new File([blob], url.pathname.split('/').pop() || 'source.png', { type: blob.type })
}

export function imageDimensions(source) {
  return new Promise((resolve, reject) => {
    const objectUrl = source instanceof Blob ? URL.createObjectURL(source) : null
    const image = new Image()
    const finish = (error) => {
      clearTimeout(timer)
      image.onload = image.onerror = null
      if (objectUrl) URL.revokeObjectURL(objectUrl)
      if (error || !image.naturalWidth || !image.naturalHeight) reject(new Error('Could not decode image dimensions'))
      else resolve({ width: image.naturalWidth, height: image.naturalHeight })
    }
    const timer = setTimeout(() => finish(true), 30000)
    image.onload = () => finish(false)
    image.onerror = () => finish(true)
    image.src = objectUrl || source
  })
}
