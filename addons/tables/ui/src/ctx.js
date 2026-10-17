// The one object every component factory takes. Nothing here is global: it is
// built from one SDK in createAddon, and it dies with that SDK. React is the
// host's own (sdk.React), so hooks run on the host's instance.
import { EN } from './i18n/en.js'
import { BUNDLES } from './i18n/index.js'

export function createContext(sdk, cache = new Map()) {
  for (const [language, bundle] of Object.entries(BUNDLES)) sdk.locale.define(language, bundle)
  const R = sdk.React
  const t = (key, vars) => sdk.locale.t(key, EN[key] === undefined ? key : EN[key], vars)
  // The last data this person read, so going back to a page is quick. It
  // holds what the person was allowed to read and nothing else, lives only
  // in this module instance and is emptied when the add-on is withdrawn.
  const ctx = {
    sdk,
    R,
    h: R.createElement,
    Fragment: R.Fragment,
    t,
    cache,
    addonId: sdk.addonId,
    // One re-render on a language change, for any component that calls it.
    useLanguage() {
      const [, set] = R.useState(0)
      R.useEffect(() => sdk.locale.subscribe(() => set(n => n + 1)), [])
    },
    clear() { cache.clear() },
  }
  // The cache goes with the SDK: when the host withdraws the add-on (sign-out,
  // another person, an update, a disable) the signal aborts and it is emptied,
  // whether or not dispose() is reached.
  sdk.onDispose(ctx.clear)
  return ctx
}
