import { readStored, writeStored } from './storage.js'

// The theme is kept in three places, each with one job:
//   ui.json `theme`            the choice itself, when the person may use that file
//   localai.theme:<account>    this browser's copy of the choice of one account
//   localai.theme              the theme last shown here, read before React runs
//                              (index.html) so a reload does not flash the other
// ui.json wins whenever it names a theme; the two browser keys only fill the
// first paint and the case where ui.json is not available.

const THEMES = ['dark', 'light']
export const isTheme = (v) => THEMES.includes(v)

// Older builds wrote the choice under this key, for everyone of the browser.
const LEGACY_KEY = 'localai-theme'

const accountName = (account) => (account ? `theme:${account}` : 'theme')

// The theme this browser holds for the account, or 'dark': dark is the
// identity of LocalAI, so an install looks like it before anyone has chosen.
// An account with no copy of its own starts from what was last shown here.
export function readTheme(account, storage) {
  const own = account ? readStored(accountName(account), [], storage) : null
  if (isTheme(own)) return own
  const shown = readStored('theme', [LEGACY_KEY], storage)
  return isTheme(shown) ? shown : 'dark'
}

export function writeTheme(account, theme, storage) {
  if (!isTheme(theme)) return
  if (account) writeStored(accountName(account), theme, storage)
  writeStored('theme', theme, storage)
}

export const themeStorageNames = (account) => ['theme', ...(account ? [accountName(account)] : [])]

// The theme to apply from ui.json, or null when nothing is to be applied:
// only a theme that differs from the one applied last counts, so a pin, a
// start page, an accent or a density saved in the same file never moves the
// theme. `applied` is undefined before the first read.
export function uiThemeToApply(applied, uiTheme) {
  if (uiTheme !== 'dark' && uiTheme !== 'light' && uiTheme !== 'system') return null
  return uiTheme === applied ? null : uiTheme
}

const MAX_TRIES = 3
let chain = Promise.resolve()

// saveUiTheme writes `theme` into ui.json and no other key, from the version
// it read, and tries again on top of a newer document after a conflict (as a
// pin does). Saves run one after the other, so the last choice is the last
// write. A failure is returned, never thrown: the choice is still kept in the
// browser.
export function saveUiTheme(theme, store) {
  const run = async () => {
    let doc = await store.load('ui')
    for (let i = 0; i < MAX_TRIES; i++) {
      try {
        await store.save('ui', { theme }, { version: doc?.version ?? 0 })
        return null
      } catch (e) {
        if (!e?.conflict) return e
        doc = e.current
      }
    }
    return new Error('ui.json keeps changing')
  }
  chain = chain.then(run, run)
  return chain
}
