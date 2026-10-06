import { SPRITE_IDS, SPRITE_PREFIX, ensureSprite } from '../components/iconSprite'
import { FALLBACK_ICON } from './faIcon'

ensureSprite()

// The same icon as components/Icon.jsx, as an HTML string, for the few places
// that build markup by hand (code-block copy buttons, artifact cards). The id
// is checked against the sprite, so the string never carries caller input.
export function iconHtml(name) {
  const id = SPRITE_IDS.has(name) ? name : FALLBACK_ICON
  return `<svg class="dk-icon lai-icon" data-icon="${id}" aria-hidden="true" focusable="false"><use href="#${SPRITE_PREFIX}${id}"></use></svg>`
}

// A slot is what the sanitizer lets through; fillIconSlots swaps each one for
// the real icon once the HTML is clean.
export const iconSlot = (name) => `<i data-icon-slot="${name}"></i>`
export const fillIconSlots = (html) =>
  html.replace(/<i data-icon-slot="([a-z0-9-]+)"><\/i>/g, (_, name) => iconHtml(name))
