/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import ConfirmDialog from '../ConfirmDialog'
import { Button, Callout } from '../ui'
import { systemApi } from '../../utils/api'
import { conflictKeys, fileSummary, missingChoices, upgradeBody, valueText } from '../../utils/modelUpdates'
import './model-updates.css'

// The confirmation of one model upgrade. It says what the upgrade does before
// anything starts: which files change, that the files download next to the old
// ones (the old version stays until it is removed), whether the model is
// loaded now and will be stopped and started again, and, for each key a
// person edited that the update also changes, a choice between their value and
// the gallery's. The Upgrade button stays off until every such key has one.
//
// `start(body)` sends the request and resolves, or rejects with the API error
// (a 409 names the keys that still need a choice in err.body.keys).
export default function ModelUpgradeDialog({ row, start, onClose }) {
  const { t } = useTranslation('updates')
  const item = useMemo(() => row.item || {}, [row.item])
  const [loaded, setLoaded] = useState(null)
  const [choices, setChoices] = useState({})
  const [configOnly, setConfigOnly] = useState(Boolean(item.configOnly))
  const [extra, setExtra] = useState([])
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  // Whether the model is loaded now: the default strategy stops it and starts
  // it again, and the dialog says so.
  useEffect(() => {
    let alive = true
    systemApi.info()
      .then((info) => {
        const list = Array.isArray(info && info.loaded_models) ? info.loaded_models : []
        if (alive) setLoaded(list.some(m => m && (m.id === row.name || m.name === row.name)))
      })
      .catch(() => { if (alive) setLoaded(null) })
    return () => { alive = false }
  }, [row.name])

  const edited = Array.isArray(item.editedKeys) ? item.editedKeys : []
  const conflicts = useMemo(() => new Set([...conflictKeys(item), ...extra]), [item, extra])
  const asked = edited.filter(k => conflicts.has(k.key))
  // A key the server named in a refusal that the read model did not list.
  for (const key of extra) if (!edited.some(k => k.key === key)) asked.push({ key, conflict: true })
  const kept = edited.filter(k => !conflicts.has(k.key))
  const missing = missingChoices(item, choices, extra)
  const files = Array.isArray(item.changedFiles) ? item.changedFiles : []
  const summary = fileSummary(item)

  const confirm = async () => {
    if (missing.length > 0 || pending) return
    setPending(true)
    setError('')
    try {
      await start(upgradeBody(item, { choices, configOnly, extra }))
      onClose()
    } catch (e) {
      const keys = e && e.body && Array.isArray(e.body.keys) ? e.body.keys : []
      if (e && e.status === 409 && keys.length > 0) {
        setExtra(prev => [...new Set([...prev, ...keys])])
        setError(t('model.dialog.needChoices'))
      } else {
        setError(t('model.dialog.startFailed', { message: (e && e.message) || t('row.noReason') }))
      }
      setPending(false)
    }
  }

  const pick = (key, value) => setChoices(prev => ({ ...prev, [key]: value }))
  const valueOrNone = (v) => {
    const text = valueText(v)
    return text == null ? <em className="mu-none">{t('model.dialog.notSet')}</em> : <code className="mu-value">{text}</code>
  }

  let loadLine
  if (loaded === true) loadLine = <Callout tone="warn" testId="model-upgrade-loaded" title={t('model.dialog.loadedTitle', { name: row.name })}>{t('model.dialog.loadedBody')}</Callout>
  else if (loaded === false) loadLine = <p className="mu-note" data-testid="model-upgrade-idle">{t('model.dialog.notLoaded', { name: row.name })}</p>
  else loadLine = <p className="mu-note" data-testid="model-upgrade-load-unknown">{t('model.dialog.loadUnknown', { name: row.name })}</p>

  const foot = (
    <>
      {missing.length > 0 && <span className="mu-foot-note" data-testid="model-upgrade-missing">{t('model.dialog.missing', { count: missing.length })}</span>}
      <Button data-autofocus onClick={onClose} disabled={pending} data-testid="model-upgrade-cancel">{t('node.cancel')}</Button>
      <Button
        variant="primary"
        icon="arrow-up"
        busy={pending}
        disabled={pending || missing.length > 0}
        onClick={confirm}
        data-testid="model-upgrade-confirm"
      >
        {loaded === true ? t('model.dialog.confirmStop') : t('model.dialog.confirm')}
      </Button>
    </>
  )

  return (
    <Dialog
      title={t('model.dialog.title', { name: row.name })}
      description={t('model.dialog.intro', { from: row.from || '?', to: row.to || '?' })}
      onClose={onClose}
      closeLabel={t('dialog.close')}
      role="alertdialog"
      pending={pending}
      wide={asked.length > 0}
      testId="model-upgrade-dialog"
      labelId="model-upgrade-title"
      foot={foot}
    >
      <div className="mu-body">
        {loadLine}

        <section className="mu-section" aria-labelledby="mu-files-title" data-testid="model-upgrade-files">
          <h3 className="mu-title" id="mu-files-title">
            {t('model.dialog.filesTitle')} <span className="mu-count">{files.length}</span>
          </h3>
          {files.length > 0 ? (
            <ul className="mu-files">
              {files.map(f => (
                <li key={`${f.change}:${f.path}`} className="mu-file" data-change={f.change}>
                  <code className="mu-file__path">{f.path}</code>
                  <span className="mu-file__meta">
                    {[t(`model.change.${f.change}`, { defaultValue: f.change }), f.role ? t(`model.role.${f.role}`, { defaultValue: f.role }) : ''].filter(Boolean).join(' · ')}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="mu-note">{t('model.dialog.noFiles')}</p>
          )}
          <p className="mu-note" data-testid="model-upgrade-disk">
            {summary.downloads > 0 && !(configOnly && item.configOnly) ? t('model.dialog.disk') : t('model.dialog.diskNone')}
          </p>
        </section>

        {summary.unchecked > 0 && (
          <Callout tone="warn" testId="model-upgrade-unchecked" title={t('model.dialog.uncheckedTitle', { count: summary.unchecked })}>
            {t('model.dialog.uncheckedBody')}
          </Callout>
        )}

        {item.configOnly && (
          <label className="mu-check" data-testid="model-upgrade-config-only">
            <input type="checkbox" className="dk-check" checked={configOnly} disabled={pending} onChange={e => setConfigOnly(e.target.checked)} />
            <span>
              <strong>{t('model.dialog.configOnly')}</strong>
              <span className="mu-check__hint">{t('model.dialog.configOnlyHint')}</span>
            </span>
          </label>
        )}

        {asked.length > 0 && (
          <section className="mu-section" aria-labelledby="mu-keys-title" data-testid="model-upgrade-keys">
            <h3 className="mu-title" id="mu-keys-title">{t('model.dialog.keysTitle')} <span className="mu-count">{asked.length}</span></h3>
            <p className="mu-note">{t('model.dialog.keysIntro')}</p>
            <ul className="mu-keys">
              {asked.map(k => (
                <li key={k.key} className="mu-key" data-testid="model-upgrade-key" data-key={k.key} data-chosen={choices[k.key] || undefined}>
                  <fieldset className="mu-key__set">
                    <legend className="mu-key__name"><code>{k.key}</code></legend>
                    <label className="mu-key__opt">
                      <input type="radio" name={`mu-${k.key}`} value="mine" checked={choices[k.key] === 'mine'} disabled={pending} onChange={() => pick(k.key, 'mine')} />
                      <span className="mu-key__text">
                        <span className="mu-key__label">{t('model.dialog.keepMine')}</span>
                        {valueOrNone(k.mine)}
                      </span>
                    </label>
                    <label className="mu-key__opt">
                      <input type="radio" name={`mu-${k.key}`} value="gallery" checked={choices[k.key] === 'gallery'} disabled={pending} onChange={() => pick(k.key, 'gallery')} />
                      <span className="mu-key__text">
                        <span className="mu-key__label">{t('model.dialog.takeGallery')}</span>
                        {valueOrNone(k.gallery)}
                      </span>
                    </label>
                  </fieldset>
                </li>
              ))}
            </ul>
          </section>
        )}

        {kept.length > 0 && (
          <p className="mu-note" data-testid="model-upgrade-kept">
            {t('model.dialog.kept', { count: kept.length, keys: kept.map(k => k.key).join(', ') })}
          </p>
        )}

        <p className="mu-note">{t('model.dialog.after')}</p>

        {error && <Callout tone="error" role="alert" testId="model-upgrade-error">{error}</Callout>}
      </div>
    </Dialog>
  )
}

// The choice of an adoption that more than one gallery entry matches: the
// server listed the candidates (gallery and entry). Nothing is recorded until
// one is chosen and confirmed. `confirm(pick)` resolves, or rejects with the
// API error, which the dialog shows.
export function ModelAdoptDialog({ row, candidates, confirm, onClose }) {
  const { t } = useTranslation('updates')
  const [choice, setChoice] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const picked = candidates.find(c => c.id === choice)

  const submit = async () => {
    if (!picked || pending) return
    setPending(true)
    setError('')
    try {
      await confirm({ gallery: picked.gallery, entry: picked.entry })
      onClose()
    } catch (e) {
      setError(t('model.adopt.failed', { message: (e && e.message) || t('row.noReason') }))
      setPending(false)
    }
  }

  const foot = (
    <>
      <Button data-autofocus onClick={onClose} disabled={pending} data-testid="model-adopt-cancel">{t('node.cancel')}</Button>
      <Button variant="primary" icon="link" busy={pending} disabled={pending || !picked} onClick={submit} data-testid="model-adopt-confirm">
        {t('model.action.adopt')}
      </Button>
    </>
  )

  return (
    <Dialog
      title={t('model.adopt.title', { name: row.name })}
      description={t('model.adopt.intro')}
      onClose={onClose}
      closeLabel={t('dialog.close')}
      role="alertdialog"
      pending={pending}
      testId="model-adopt-dialog"
      labelId="model-adopt-title"
      foot={foot}
    >
      <div className="mu-body">
        <fieldset className="mu-adopt" disabled={pending}>
          <legend className="mu-title">{t('model.adopt.legend', { count: candidates.length })}</legend>
          {candidates.map(c => (
            <label key={c.id} className="mu-adopt__opt" data-testid="model-adopt-option" data-candidate={c.id}>
              <input type="radio" name="mu-adopt" value={c.id} checked={choice === c.id} onChange={() => setChoice(c.id)} />
              <span className="mu-key__text">
                <span className="mu-key__label">{c.entry}</span>
                {c.gallery && <span className="mu-note">{t('model.adopt.gallery', { gallery: c.gallery })}</span>}
              </span>
            </label>
          ))}
        </fieldset>
        <p className="mu-note">{t('model.adopt.after')}</p>
        {error && <Callout tone="error" role="alert" testId="model-adopt-error">{error}</Callout>}
      </div>
    </Dialog>
  )
}

// The dialogs of the model actions (useModelUpgradeActions): the upgrade
// confirmation, the adoption choice and the confirmation of "Remove old version", which deletes
// files.
export function ModelActionDialogs({ model }) {
  const { t } = useTranslation('updates')
  const row = model.confirming
  const rm = model.removing
  return (
    <>
      {model.adopting && (
        <ModelAdoptDialog key={model.adopting.row.id} row={model.adopting.row} candidates={model.adopting.candidates} confirm={model.confirmAdopt} onClose={model.closeAdopt} />
      )}
      {row && <ModelUpgradeDialog key={row.id} row={row} start={(body) => model.start(row, body)} onClose={model.closeConfirm} />}
      <ConfirmDialog
        open={Boolean(rm)}
        danger
        title={rm ? t('model.remove.title', { name: rm.name }) : ''}
        message={rm ? t('model.remove.body', { count: rm.retained }) : ''}
        confirmLabel={t('model.remove.confirm')}
        pendingLabel={t('model.remove.pending')}
        pending={model.removingPending}
        onConfirm={model.confirmRemoveOld}
        onCancel={model.closeRemove}
      />
    </>
  )
}
