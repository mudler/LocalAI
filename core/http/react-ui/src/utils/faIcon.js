import faMap from '../vendor/ui-kit/icons/fa-map.json'

// The icon drawn for a name the map does not know.
export const FALLBACK_ICON = 'circle'

const STYLE_CLASSES = new Set(['fa', 'fas', 'far', 'fab', 'fa-solid', 'fa-regular', 'fa-brands'])
const MODIFIER = /^fa-(spin|fw|lg|xs|sm|[0-9]x|beat|beat-fade|pulse)$/
const NAME_OVERRIDES = { github: 'github', 'apple-whole': 'apple' }

// Resolve a Font Awesome class string or bare name to a kit icon id.
//   iconFromFa('fas fa-trash')       -> 'trash'
//   iconFromFa('fa-spinner fa-spin') -> 'spinner'
//   iconFromFa('does-not-exist')     -> 'circle'
// A name that is already a kit icon id is returned unchanged; the Icon
// component draws the fallback for any id the sprite does not hold.
export function iconFromFa(value) {
  const word = String(value || '')
    .split(/\s+/)
    .find((t) => t && !STYLE_CLASSES.has(t) && !MODIFIER.test(t))
  if (!word) return FALLBACK_ICON
  const bare = word.replace(/^fa-/, '')
  if (NAME_OVERRIDES[bare]) return NAME_OVERRIDES[bare]
  if (bare !== 'unmapped' && Object.hasOwn(faMap, bare)) return faMap[bare]
  return bare
}

export const faSpins = (value) => /(^|\s)fa-spin(\s|$)/.test(String(value || ''))
