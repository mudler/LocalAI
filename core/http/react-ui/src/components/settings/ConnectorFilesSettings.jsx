import { useEffect, useMemo, useState } from 'react'
import { createHostSettingsClient } from '../../utils/hostSettingsClient'
import { createAddonsApi } from '../../utils/addonsApi'
import { chatConnectorsApi } from '../../utils/api'
import { apiUrl } from '../../utils/basePath'
import { bytesToMib, formatFileSize, mibToBytes } from '../../utils/fileSize'
import { useHostSettings } from '../../hooks/useHostSettings'

const MIN_BYTES = 1024 // mirrors hostsettings.MinFilesCeiling
const MAX_BYTES = 2 ** 40

// The host ceiling of the files that connector add-ons move: the owner policy
// key connector_files_ceiling_bytes. No bot can go above it. The numbers in
// effect come from the server, so the page names no limit of its own.
export default function ConnectorFilesSettings() {
  const client = useMemo(() => createHostSettingsClient({ api: createAddonsApi({ base: apiUrl }) }), [])
  const owner = useHostSettings(client, 'connector-files')
  const [files, setFiles] = useState(null)
  const [text, setText] = useState('')
  const saved = owner.snapshot?.policy.connector_files_ceiling_bytes

  useEffect(() => {
    chatConnectorsApi.kinds().then((r) => setFiles(r.kinds?.[0]?.files || null)).catch(() => {})
  }, [saved])
  useEffect(() => { if (saved !== undefined) setText(bytesToMib(saved)) }, [saved])

  const bytes = mibToBytes(text)
  const bad = text.trim() !== '' && (bytes < MIN_BYTES || bytes > MAX_BYTES)
  return <section aria-label="Connector files" data-testid="owner-connector-files">
    <label>Largest file a connector may move, in MiB{' '}
      <input className="dk-input" type="number" min="0" step="any" inputMode="decimal" value={text} data-testid="connector-files-ceiling"
        placeholder="Default" disabled={!owner.snapshot || owner.busy || !!owner.error} onChange={e => setText(e.target.value)} />
    </label>
    <p className="st-note">No bot can send or read a larger file, whatever its add-on or its owner setting says. Leave it empty for the default.
      {files?.host_ceiling_bytes > 0 && <> Now: {formatFileSize(files.host_ceiling_bytes)}.</>}</p>
    {bad && <p className="st-note" role="alert">Use a size from {formatFileSize(MIN_BYTES)} to {formatFileSize(MAX_BYTES)}, or leave it empty.</p>}
    {owner.error && <div role="alert">Owner settings unavailable or changed: {owner.error.message}
      <button type="button" disabled={owner.busy} onClick={owner.reload}>Reload and rebase edits</button>
      {owner.uncertain && <button type="button" disabled={owner.busy} onClick={() => owner.save({})}>Retry same operation</button>}
    </div>}
    <button type="button" disabled={!owner.snapshot || owner.busy || !!owner.error || bad || bytes === saved}
      onClick={() => owner.save({ connector_files_ceiling_bytes: bytes })}>Apply file ceiling</button>
  </section>
}
