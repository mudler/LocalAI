/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The limits of the files a bot reads and sends. The owner can only lower the
// limit of the connector kind. The page shows the limit in effect with the
// layer that sets it. The numbers come from the server; the words live here as
// defaults, like the rest of the shared fields.
import { Field, Hint, TextInput } from './controls'
import { fileDirection, fileLimitProblem, formatBytes, previewLimit } from './connector'

// The limit that a field shows: what the typed value gives over the limit of
// the kind, or the saved limit of the binding when the kind is not known.
function shown(base, saved, mib) {
  return previewLimit(base, mib) || (saved ? { bytes: saved.effective_bytes, layer: saved.layer } : null)
}

function Limit({ label, help, mib, onChange, base, saved, s, testId }) {
  const dir = fileDirection(base)
  const problem = dir === 'off' ? '' : fileLimitProblem(mib, base?.allowed_bytes)
  const limit = dir === 'off' ? null : shown(base, saved, mib)
  const error = problem === 'invalid' ? s('files.invalid') : problem === 'tooHigh' ? s('files.tooHigh', { max: formatBytes(base.allowed_bytes) }) : undefined
  const declared = dir === 'declared' ? s('files.addon.declared', { max: formatBytes(base.declared_bytes) })
    : dir === 'open' ? s('files.addon.open') : dir === 'off' ? s('files.addon.off') : ''
  const now = limit ? s('files.now', { limit: formatBytes(limit.bytes), layer: s(`files.layer.${limit.layer}`) }) : ''
  return (
    <Field label={label} error={error} hint={[help, declared, now].filter(Boolean).join(' ')}>
      {(p) => (
        <TextInput {...p} type="number" inputMode="decimal" min="0" step="any" data-testid={testId} disabled={dir === 'off'}
          value={dir === 'off' ? '' : mib} onChange={(e) => onChange(e.target.value)} />
      )}
    </Field>
  )
}

/**
 * @param {object} props
 * @param {object} props.form    the form of the binding
 * @param {Function} props.setForm
 * @param {object} [props.kind]  the files of the kind, from the kind list
 * @param {object} [props.saved] the files of the saved binding, when there is one
 * @param {Function} props.s     the translation function with the defaults
 */
export default function FilesFields({ form, setForm, kind, saved, s }) {
  const hostCeiling = kind?.host_ceiling_bytes || 0
  return (
    <fieldset className="cn-group" data-testid="connector-filelimits">
      <legend>{s('files.title')}</legend>
      <p className="dk-hint">{s('files.help')}</p>
      {hostCeiling > 0 && <Hint>{s('files.host', { max: formatBytes(hostCeiling) })}</Hint>}
      <Limit label={s('files.receiveLabel')} help={s('files.receiveHelp')} mib={form.file_receive_mib} base={kind?.receive} saved={saved?.receive} s={s} testId="connector-file-receive"
        onChange={(v) => setForm({ ...form, file_receive_mib: v })} />
      <Limit label={s('files.sendLabel')} help={s('files.sendHelp')} mib={form.file_send_mib} base={kind?.send} saved={saved?.send} s={s} testId="connector-file-send"
        onChange={(v) => setForm({ ...form, file_send_mib: v })} />
    </fieldset>
  )
}
