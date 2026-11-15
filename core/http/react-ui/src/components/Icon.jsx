import Icon from '../../packages/ui-kit/src/Icon.jsx'
import { ensureSprite } from './iconSprite'
import { iconFromFa, faSpins } from '../utils/faIcon'

// The kit icon (packages/ui-kit/src/Icon.jsx) draws inline. The sprite is
// still put into the page once, for the HTML strings that iconHtml() builds
// by hand (code-block copy buttons, artifact cards), which point into it.
ensureSprite()

export default Icon

// An icon named the Font Awesome way, for names that arrive at run time (a
// server-supplied icon, a stored preference). Accepts "robot", "fa-robot",
// "fas fa-robot" or "fa-solid fa-spinner fa-spin"; the spin modifier turns on
// the spin prop. Unknown names draw the fallback icon.
export function FaIcon({ name, spin, ...props }) {
  return <Icon name={iconFromFa(name)} spin={spin ?? faSpins(name)} {...props} />
}
