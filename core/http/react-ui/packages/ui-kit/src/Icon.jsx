import { ICONS } from './icons.js'
import { BRAND_GLYPHS } from './brandGlyphs.jsx'

// The icon drawn for an id the set does not hold.
export const FALLBACK_ICON = 'circle'

// Every outline icon id of the kit set (the brand marks are not in it).
export const ICON_IDS = new Set(Object.keys(ICONS))

export const hasIcon = (name) => ICON_IDS.has(name) || Object.hasOwn(BRAND_GLYPHS, name)

// One icon from the kit's outline set, drawn inline: it needs no sprite in the
// document, so it works the same in a portal, a shadow root or another window.
// It is sized by the font size (1.2em, see css/kit.css), coloured by
// `currentColor` and needs no font file.
//
//   name      kit icon id, for example "trash" or "chevron-down"
//   size      number (pixels) or CSS length; default follows font-size
//   spin      rotate the icon, for busy states
//   title     accessible name; without it the icon is hidden from assistive tech
//
// An unknown id draws FALLBACK_ICON instead of an empty box. The group around
// the drawing carries the attributes of the kit's symbols, so a page rule that
// sets `fill` on the svg does not fill an outline icon.
export default function Icon({ name, size, className, title, spin = false, style, ...rest }) {
  const glyph = Object.hasOwn(BRAND_GLYPHS, name) ? BRAND_GLYPHS[name] : null
  const id = glyph || ICON_IDS.has(name) ? name : FALLBACK_ICON
  const classes = ['lai-icon', spin && 'dk-spin', glyph && 'lai-icon--glyph', className]
    .filter(Boolean)
    .join(' ')
  const dim = typeof size === 'number' ? `${size}px` : size
  const sized = dim ? { width: dim, height: dim, ...style } : style
  const a11y = title ? { role: 'img' } : { 'aria-hidden': 'true' }
  return (
    <svg
      className={classes}
      style={sized}
      data-icon={id}
      focusable="false"
      viewBox="0 0 24 24"
      {...a11y}
      {...rest}
    >
      {title ? <title>{title}</title> : null}
      {glyph || (
        <g
          fill="none"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          dangerouslySetInnerHTML={{ __html: ICONS[id] }}
        />
      )}
    </svg>
  )
}
