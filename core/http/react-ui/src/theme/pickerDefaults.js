// Starting values of the colour and gradient pickers (BackgroundField). They
// are saved into ui.json as hex strings, and <input type="color"> only takes
// hex, so a CSS role such as var(--dk-accent) cannot stand in. They live in
// src/theme/ because the colour gate treats that folder as the palette.
export const PICKER_FALLBACK = { color: '#1e293b', from: '#0f172a', to: '#3b82f6' }
