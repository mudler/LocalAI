// SPDX-License-Identifier: MIT
import { createAddonsApi } from './addonsApi.js'
import { newOperationId } from '../components/catalog/catalogState.js'

export class HostSettingsError extends Error {
  constructor(kind, message = kind) { super(message); this.kind = kind }
}

// No actor or authority claims are accepted here. The dispatch handler derives
// them from the session. A prepared write is immutable and safe to replay.
export function createHostSettingsClient({ api = createAddonsApi() } = {}) {
  async function dispatch(request, signal) {
    const envelope = await api.dispatch(request, signal)
    if (envelope?.error) throw new HostSettingsError(envelope.error)
    const dto = envelope?.data
    if (dto?.schema_version !== 1 || !Number.isSafeInteger(dto.revision) || dto.revision < 1 || !dto.policy || typeof dto.policy !== 'object' || Array.isArray(dto.policy)) {
      throw new HostSettingsError('unavailable', 'Owner settings are unavailable')
    }
    return { schema_version: dto.schema_version, revision: dto.revision, policy: structuredClone(dto.policy) }
  }
  return {
    read: signal => dispatch({ action: 'host-settings-read', operationId: newOperationId(), args: {} }, signal),
    prepare(patch, expectedRevision) {
      // Capture serialized bytes, not a caller-owned mutable map.
      const body = JSON.stringify({ expectedRevision, patch })
      const operationId = newOperationId()
      return Object.freeze({ operationId, update: signal => dispatch({ action: 'host-settings-update', operationId, args: JSON.parse(body) }, signal) })
    },
  }
}
