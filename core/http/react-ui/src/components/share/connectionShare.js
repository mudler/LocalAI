// SPDX-License-Identifier: MIT
// The start of sharing a connector binding from an add-on page: the host asks
// for the ID of the binding in the sharing API itself. No React and no network
// of its own: the node unit tests drive it directly.

const BINDING = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/

// connectionShareStart returns the ID of the binding in /api/shares/connection.
// api is the host's chatConnectorsApi (utils/api.js).
export async function connectionShareStart(api, binding) {
  if (typeof binding !== 'string' || !BINDING.test(binding)) throw new TypeError('A connector binding is required')
  const r = await api.share(binding)
  const id = r && typeof r.resource_id === 'string' ? r.resource_id : ''
  if (!id) throw new Error('The binding cannot be shared')
  return id
}
