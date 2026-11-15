import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import SideSheet from '../SideSheet'
import ConfirmDialog from '../ConfirmDialog'
import Button from '../ui/Button'
import Callout from '../ui/Callout'
import Segmented from '../ui/Segmented'
import { remotesApi } from '../../utils/api'
import NoUsable from './NoUsable'
import { createErrorKind, noUsableModels } from '../../utils/remotes'

// Edit one remote: its name, its address and a new key. The key is
// write-only: the server never sends it back, so the field is empty and an
// empty field keeps the key that is set. A new key restarts the loaded models.
// It says so when the remote answers yet has no usable model.
// "Remove remote" asks what happens to the models first.
export default function EditRemoteSheet({ remote, onClose, onSaved, onRemoved }) {
  const { t } = useTranslation('remotes')
  const uid = useId()
  const [name, setName] = useState(remote.name)
  const [url, setUrl] = useState(remote.url)
  const [key, setKey] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)
  const [confirming, setConfirming] = useState(false)
  // Nothing is deleted unless the person says so.
  const [entries, setEntries] = useState('keep')
  const [removing, setRemoving] = useState(false)
  const fixedUrl = remote.kind === 'anthropic'

  const body = {}
  if (name.trim() && name.trim() !== remote.name) body.name = name.trim()
  if (!fixedUrl && url.trim() && url.trim() !== remote.url) body.url = url.trim()
  if (key.trim()) body.api_key = key.trim()
  const dirty = Object.keys(body).length > 0

  const save = async (e) => {
    e?.preventDefault()
    if (!dirty || saving) return
    setSaving(true); setError(null)
    try {
      const updated = await remotesApi.update(remote.id, body)
      onSaved?.(updated, { keyChanged: !!body.api_key, refreshed: !!(body.api_key || body.url) })
    } catch (err) {
      setError({ kind: createErrorKind(err), message: err.message || '' })
    } finally {
      setSaving(false)
    }
  }

  const remove = async () => {
    setRemoving(true)
    try {
      await remotesApi.remove(remote.id, entries)
      setConfirming(false)
      onRemoved?.(remote, entries)
    } catch (err) {
      setConfirming(false)
      setError({ kind: 'other', message: err.message || '' })
    } finally {
      setRemoving(false)
    }
  }

  const choices = [
    { id: 'keep', label: t('remove.keep') },
    { id: 'remove', label: t('remove.removeAll') },
  ]

  return (
    <>
      <SideSheet
        title={t('edit.title')}
        description={remote.name}
        labelId="edit-remote-title"
        testId="edit-remote"
        closeLabel={t('edit.close')}
        onClose={onClose}
        footer={(
          <>
            <Button onClick={onClose}>{t('edit.cancel')}</Button>
            <Button variant="primary" busy={saving} disabled={!dirty} onClick={save} data-testid="edit-save">{t('edit.save')}</Button>
          </>
        )}
      >
        <form className="remotes-form" onSubmit={save} noValidate>
          {noUsableModels(remote) && <NoUsable remote={remote} />}
          <div className="dk-field">
            <label className="dk-label" htmlFor={`${uid}-name`}>{t('edit.name')}</label>
            <input id={`${uid}-name`} className="dk-input" autoComplete="off" value={name} onChange={e => setName(e.target.value)} data-testid="edit-name" aria-invalid={error?.kind === 'nameTaken' || undefined} />
          </div>
          {!fixedUrl && (
            <div className="dk-field">
              <label className="dk-label" htmlFor={`${uid}-url`}>{t('connect.address')}</label>
              <input id={`${uid}-url`} className="dk-input" type="url" autoComplete="off" spellCheck={false} value={url} onChange={e => setUrl(e.target.value)} data-testid="edit-url" />
            </div>
          )}
          <div className="dk-field">
            <label className="dk-label" htmlFor={`${uid}-key`}>{t('edit.key')}</label>
            <input
              id={`${uid}-key`}
              className="dk-input"
              type="password"
              autoComplete="new-password"
              spellCheck={false}
              value={key}
              onChange={e => setKey(e.target.value)}
              aria-describedby={`${uid}-key-hint`}
              data-testid="edit-key"
            />
            <p className="dk-hint" id={`${uid}-key-hint`} data-testid="edit-key-hint">
              {remote.key_set ? t('edit.keySet') : t('edit.keyNotSet')} {t('edit.keyRestart')}
            </p>
          </div>
          {error && (
            <Callout tone="error" role="alert" testId="edit-error">{t(`choose.error.${error.kind}`, { message: error.message })}</Callout>
          )}
          <div className="remotes-danger">
            <Button variant="danger-ghost" icon="trash" onClick={() => { setEntries('keep'); setConfirming(true) }} data-testid="edit-remove">{t('remove.button')}</Button>
          </div>
        </form>
      </SideSheet>
      <ConfirmDialog
        open={confirming}
        danger
        title={t('remove.title', { name: remote.name })}
        message={(
          <div className="remotes-remove">
            <Segmented mode="radio" label={t('remove.choiceLabel')} options={choices} value={entries} onChange={setEntries} testId="remove-choice" itemTestId="remove-choice-" />
            <p className="remotes-hint" data-testid="remove-explain">
              {entries === 'keep'
                ? t('remove.keepExplain', { count: remote.entries?.length || 0 })
                : t('remove.removeExplain', { count: remote.entries?.length || 0 })}
            </p>
          </div>
        )}
        confirmLabel={t('remove.confirm')}
        pending={removing}
        onConfirm={remove}
        onCancel={() => setConfirming(false)}
      />
    </>
  )
}
