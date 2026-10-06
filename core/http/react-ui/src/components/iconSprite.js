import spriteSource from '../vendor/ui-kit/icons/sprite.svg?raw'

// Every symbol id in the vendored sprite, without the dk-icon- prefix.
export const SPRITE_PREFIX = 'dk-icon-'
export const SPRITE_IDS = new Set(
  Array.from(spriteSource.matchAll(/id="dk-icon-([^"]+)"/g), (m) => m[1]),
)

const HOLDER_ID = 'lai-icon-sprite'

// Put the sprite into the page once, so that <use href="#dk-icon-x"> resolves
// from any component or portal. An inline sprite needs no request and has no
// dependence on the base path the app is served from (root, reverse-proxy
// subpath or the Go-embedded build).
export function ensureSprite() {
  if (typeof document === 'undefined' || !document.body) return
  if (document.getElementById(HOLDER_ID)) return
  const holder = document.createElement('div')
  holder.id = HOLDER_ID
  holder.className = 'lai-icon-sprite'
  holder.setAttribute('aria-hidden', 'true')
  holder.innerHTML = spriteSource
  document.body.prepend(holder)
}
