// Starting colours of the background editor. A background is saved in ui.json
// as plain #rrggbb values, because a native colour input cannot take a CSS
// variable. The editor needs a literal start value when a user picks the
// colour or gradient kind, so the values live here, in the theme folder the
// colour gate does not scan, and not in the page code.
export const BACKGROUND_FALLBACK = { color: '#1e293b', from: '#0f172a', to: '#3b82f6' }
