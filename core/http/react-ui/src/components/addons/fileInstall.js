// Pure helpers of the install from a file: what the owner found in an
// uploaded zip, shaped for the install dialog that library entries use.

// The source of a package that came from a file. Nothing signs it.
export const LOCAL_FILE = 'local-file'

export function isLocalFile(sourceId) {
  return sourceId === LOCAL_FILE
}

const RELATIONS = new Set(['new', 'upgrade', 'same', 'downgrade'])

const list = (v) => (Array.isArray(v) ? v.filter(x => typeof x === 'string') : [])
const text = (v, max = 200) => (typeof v === 'string' ? v.slice(0, max) : '')

// The answer of the upload, checked field by field. Anything the owner did
// not send in the expected shape is dropped, never guessed.
export function normalizeFile(raw) {
  const v = raw && typeof raw === 'object' ? raw : {}
  const api = v.api && typeof v.api === 'object' ? v.api : {}
  return {
    fileId: text(v.fileId, 64),
    name: text(v.name),
    size: Number.isFinite(v.size) ? v.size : 0,
    sha256: text(v.sha256, 64).toLowerCase(),
    addonId: text(v.addonId, 64),
    title: text(v.title) || text(v.addonId, 64),
    version: text(v.version, 64),
    platform: text(v.platform, 32),
    api: { min: Number.isFinite(api.min) ? api.min : 0, max: Number.isFinite(api.max) ? api.max : 0 },
    required: list(v.required),
    optional: list(v.optional),
    installed: v.installed === true,
    installedVersion: text(v.installedVersion, 64),
    relation: RELATIONS.has(v.relation) ? v.relation : 'new',
  }
}

// The entry the install dialog works on. The dialog asks the owner what the
// file needs from the host with the same preview as for a library entry: the
// source is local-file and the version is the id of the staged file.
export function fileEntry(file) {
  return {
    key: `${LOCAL_FILE}/${file.addonId}/${file.fileId}`,
    id: file.addonId,
    title: file.title,
    sourceId: LOCAL_FILE,
    version: file.fileId,
    digest: `sha256:${file.sha256}`,
    policy: { requireSignature: false },
    shadows: [],
    local: file,
  }
}

// Whether the file may be installed at all, and what the person must confirm
// first. The same version is never installed again; an older one needs a tick.
export function fileGate(file, { downgradeTicked = false, locked = false } = {}) {
  if (locked) return { allowed: false, reason: 'locked' }
  if (!file || file.relation === 'same') return { allowed: false, reason: 'same' }
  if (file.relation === 'downgrade' && !downgradeTicked) return { allowed: false, reason: 'downgrade' }
  return { allowed: true, reason: '' }
}

// The lifecycle action the file needs: an installed add-on is upgraded.
export function fileAction(file) {
  return file && file.installed ? 'upgrade' : 'install'
}

// The file id a link carries in ?file=, or '' when it has not the shape of one.
export function fileIdFromParams(params) {
  const id = params && typeof params.get === 'function' ? params.get('file') : ''
  return /^[0-9a-f]{32}$/.test(id || '') ? id : ''
}
