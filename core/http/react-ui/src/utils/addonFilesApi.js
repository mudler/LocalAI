// Client of the add-on file routes: an add-on zip is staged on the owner first,
// then installed through the host access install. Staging runs nothing; the
// owner keeps the file for an hour, for the person who uploaded it.
import { classifyHostGrants } from './hostGrantsApi.js'

// The id of a staged file: 32 lower-case hex characters.
export const FILE_ID = /^[0-9a-f]{32}$/

// The rules a file can break, and the answers about a staged file. Each is a
// final answer with words of its own from the owner.
const TYPES = new Set([
  'bad_zip', 'unsafe_path', 'unsupported_entry', 'too_large', 'too_many_files', 'compression_ratio',
  'bad_manifest', 'digest_mismatch', 'wrong_platform', 'bad_mode', 'not_found', 'busy', 'invalid_request',
])

export function classifyFiles(status, body) {
  const err = body && typeof body === 'object' ? body.error : null
  const type = err && typeof err === 'object' && typeof err.type === 'string' ? err.type : ''
  if (TYPES.has(type)) {
    return { kind: 'invalid', type, status, message: typeof err.message === 'string' ? err.message : '', details: err }
  }
  return classifyHostGrants(status, body)
}

export function createAddonFilesApi(addonsApi) {
  const call = (path, opts = {}) => addonsApi.call(`/api/addons/files${path}`, { classify: classifyFiles, ...opts })
  return {
    // Sends the file and answers with what the owner found in it.
    upload(file, signal) {
      const form = new FormData()
      form.append('file', file, file.name)
      return call('', { method: 'POST', body: form, signal })
    },
    describe(id, signal) { return call(`/${encodeURIComponent(id)}`, { signal }) },
    async remove(id, signal) { await call(`/${encodeURIComponent(id)}`, { method: 'DELETE', signal }) },
  }
}
