// SPDX-License-Identifier: MIT
import { createAddonsApi } from './addonsApi.js'
import { newOperationId } from '../components/catalog/catalogState.js'

export class HostSettingsError extends Error {
  constructor(kind, message = kind) { super(message); this.kind = kind }
}

const POLICY_STRINGS = ['chats_egress_ceiling', 'chats_egress_default', 'chats_egress_ports', 'chats_egress_extra_registries', 'chats_egress_allowed_hosts']
const POLICY_BOOLEANS = ['chats_egress_allow_private', 'auto_upgrade_addons']
const POLICY_NUMBERS = ['connector_files_ceiling_bytes']
const MAX_FILES_CEILING = 2 ** 40 // mirrors contract.MaxDeclaredFileBytes
const POLICY_KEYS = [...POLICY_STRINGS, ...POLICY_BOOLEANS, ...POLICY_NUMBERS, 'chats_tool_providers']
const DEFINITE_ERRORS = ['invalid', 'revision_conflict', 'operation_mismatch']
const plain = value => value !== null && typeof value === 'object' && [Object.prototype, null].includes(Object.getPrototypeOf(value))
const exact = (value, keys) => plain(value) && Reflect.ownKeys(value).length === keys.length && keys.every(key => Object.hasOwn(value, key))
const byteLength = value => new TextEncoder().encode(value).length

function validPolicy(policy) {
  // Policy.MarshalJSON writes every key (zero means the default) and emits {} for pins.
  // Do not turn missing/unknown fields into frontend defaults. A future owner
  // schema needs an explicit client migration rather than a partial snapshot.
  if (!exact(policy, POLICY_KEYS)) return false
  if (!POLICY_STRINGS.every(key => typeof policy[key] === 'string' && policy[key].length <= 8192 && byteLength(policy[key]) <= 8192)) return false
  if (!POLICY_BOOLEANS.every(key => typeof policy[key] === 'boolean')) return false
  if (!POLICY_NUMBERS.every(key => Number.isSafeInteger(policy[key]) && policy[key] >= 0 && policy[key] <= MAX_FILES_CEILING)) return false
  if (!['chats_egress_ceiling', 'chats_egress_default'].every(key => ['', 'off', 'registries', 'all'].includes(policy[key].trim()))) return false
  const pins = policy.chats_tool_providers
  if (!plain(pins) || Reflect.ownKeys(pins).length > 128) return false
  for (const key of Reflect.ownKeys(pins)) {
    if (typeof key !== 'string' || !key || byteLength(key) > 128 || typeof pins[key] !== 'string' || !pins[key] || pins[key].length > 128 || byteLength(pins[key]) > 128) return false
  }
  // Bounds mirror hostsettings.validate. Ports, host syntax and provider
  // membership remain server-authoritative, not a second permission engine.
  // Allow JSON escaping overhead even when all bounded strings are populated.
  return byteLength(JSON.stringify(policy)) <= 512 * 1024
}

// No actor or authority claims are accepted here. The dispatch handler derives
// them from the session. A prepared write is immutable and safe to replay.
export function createHostSettingsClient({ api = createAddonsApi() } = {}) {
  async function dispatch(request, signal) {
    const envelope = await api.dispatch(request, signal)
    if (exact(envelope, ['error']) && DEFINITE_ERRORS.includes(envelope.error)) throw new HostSettingsError(envelope.error)
    const dto = envelope?.data
    if (!exact(envelope, ['data']) || !exact(dto, ['schema_version', 'revision', 'policy']) || dto.schema_version !== 1 || !Number.isSafeInteger(dto.revision) || dto.revision < 1 || !validPolicy(dto.policy)) {
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
