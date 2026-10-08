import { BRAND_GLYPHS } from './iconGlyphs'
import { SPRITE_IDS, SPRITE_PREFIX, ensureSprite } from './iconSprite'
import { iconFromFa, faSpins, FALLBACK_ICON } from '../utils/faIcon'

ensureSprite()

// One icon from the shared kit's outline set. It draws an inline <svg> that
// points at the sprite, so it is sized by the font size (1em), coloured by
// `currentColor` and needs no font file.
//
//   name      kit icon id, for example "trash" or "chevron-down"
//   size      number (pixels) or CSS length; default 1em, follows font-size
//   spin      rotate the icon, for busy states
//   title     accessible name; without it the icon is hidden from assistive tech
//
// An unknown id draws FALLBACK_ICON instead of an empty box.
export default function Icon({ name, size, className, title, spin = false, style, ...rest }) {
  const glyph = BRAND_GLYPHS[name]
  const id = glyph || SPRITE_IDS.has(name) ? name : FALLBACK_ICON
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
      {glyph || <use href={`#${SPRITE_PREFIX}${id}`} />}
    </svg>
  )
}

// An icon named the Font Awesome way, for names that arrive at run time (a
// server-supplied icon, a stored preference). Accepts "robot", "fa-robot",
// "fas fa-robot" or "fa-solid fa-spinner fa-spin"; the spin modifier turns on
// the spin prop. Unknown names draw the fallback icon.
export function FaIcon({ name, spin, ...props }) {
  return <Icon name={iconFromFa(name)} spin={spin ?? faSpins(name)} {...props} />
}
