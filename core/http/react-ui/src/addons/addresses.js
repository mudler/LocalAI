// SPDX-License-Identifier: MIT
// Page may include a declared nested suffix. Query and hash belong to the
// caller; model is always a value, never a route segment or query template.
export function addonPageAddress({ addon, page, model, search = '', hash = '' }) {
  const params = new URLSearchParams(search)
  if (model !== undefined && model !== null) params.set('model', model)
  const query = params.toString()
  return `/app/addons/${encodeURIComponent(addon)}/${page}${query ? `?${query}` : ''}${hash}`
}
