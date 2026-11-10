import { accentCheck, themePalette } from '../../utils/look'
import themeFile from '../../theme/theme-localai.json'

function systemMode() {
  try { return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark' } catch { return 'dark' }
}

// pageMode is the mode (light or dark) the page shows for a theme value of
// ui.json: the one it names, the device's for system, or the one on the page
// when the file names none.
export function pageMode(theme) {
  if (theme === 'dark' || theme === 'light') return theme
  if (theme === 'system') return systemMode()
  try { return document.documentElement.getAttribute('data-theme') === 'light' ? 'light' : 'dark' } catch { return 'dark' }
}

// paletteOf is the colours of the theme file for a mode.
export const paletteOf = (mode) => themePalette(themeFile, mode)

// accentContrast checks an accent against the page of the mode a theme value
// gives: {ok, ratio, mode}, or null when there is no accent to check.
export function accentContrast(accent, theme) {
  if (typeof accent !== 'string' || !/^#[0-9a-fA-F]{6}$/.test(accent)) return null
  const mode = pageMode(theme)
  return { ...accentCheck(accent, paletteOf(mode)), mode }
}
