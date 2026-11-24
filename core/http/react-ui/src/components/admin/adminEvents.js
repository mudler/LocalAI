// The Search button of the Administration sidebar sits outside the layout that
// owns the command palette, so it asks for the palette with a window event.
export const OPEN_PALETTE_EVENT = 'localai:admin-palette'

export function openAdminPalette() {
  window.dispatchEvent(new CustomEvent(OPEN_PALETTE_EVENT))
}
