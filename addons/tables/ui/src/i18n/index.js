import { EN } from './en.js'
import { LOCALES } from './locales.js'

// Every language of the host: English first, then the translations. A key a
// translation does not have is read from English by the SDK.
export const BUNDLES = Object.freeze({ en: EN, ...LOCALES })
