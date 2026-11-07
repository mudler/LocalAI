/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, Callout } from '../ui'
import { MAX_NAME } from '../../utils/setups'
import './setups.css'

// The line under the Home composer while a setup is in use (or a name is being
// asked): "Setup · model · tools · Effort level" with the loaded dot, and when
// the composer was edited, Update, Save as new and Reset. Save as new turns
// the actions into a name field.
//
//   active        the setup in use, or null (only the name field shows)
//   edited        the composer differs from it
//   model, toolLabels, effort, warm   what the composer holds now
//   warning       text of a problem with the setup (its model is missing)
//   naming        the name field is open
//   onUpdate(), onStartNaming(), onCancelNaming(), onReset(); onSaveAs(name)
//   answers true when it was saved (the name field keeps the text otherwise)
export default function SetupStatusLine({
  active, edited, model, toolLabels, effort, warm, warning, naming, busy = false,
  onUpdate, onStartNaming, onCancelNaming, onSaveAs, onReset,
}) {
  const { t } = useTranslation('chat')
  const [name, setName] = useState('')
  const level = t(`settings.effort.${effort || 'default'}`)
  // Up to three names; more than that is a count. Never "No tools" when
  // the Tools badge counts something: both read the same list.
  const tools = toolLabels.length === 0 ? t('setup.line.noTools')
    : toolLabels.length <= 3 ? toolLabels.join(', ') : t('setup.line.toolCount', { count: toolLabels.length })
  const [saving, setSaving] = useState(false)
  // The name stays in the field until the save worked, so a refusal (the limit,
  // a server error) does not lose it.
  const submit = async (e) => {
    e.preventDefault()
    if (!name.trim() || saving) return
    setSaving(true)
    const ok = await onSaveAs(name)
    setSaving(false)
    if (ok) setName('')
  }
  return (
    <>
      <div className="cx-ready setup-line" data-testid="setup-line">
        {active && (
          <span className="setup-line__what">
            <span className={`home-dot${warm ? '' : ' home-dot--cold'}`} aria-hidden="true" />
            <span className="setup-line__text" data-testid="setup-line-text">
              <span className="setup-line__name">{active.name}</span>
              {' · '}{model}{' · '}{tools}{' · '}{t('setup.effort', { level })}
            </span>
          </span>
        )}
        {naming ? (
          <form className="setup-line__form" onSubmit={submit} data-testid="setup-name-form">
            <input
              className="dk-input"
              autoFocus
              value={name}
              maxLength={MAX_NAME}
              aria-label={t('setup.nameLabel')}
              placeholder={t('setup.namePlaceholder')}
              data-testid="setup-name-input"
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Escape') { e.stopPropagation(); setName(''); onCancelNaming() } }}
            />
            <Button size="sm" variant="primary" type="submit" busy={busy || saving} disabled={!name.trim()} data-testid="setup-name-save">{t('setup.save')}</Button>
            <Button size="sm" variant="ghost" onClick={() => { setName(''); onCancelNaming() }} data-testid="setup-name-cancel">{t('setup.cancel')}</Button>
          </form>
        ) : (active && edited && (
          <span className="setup-line__actions" data-testid="setup-line-actions">
            <Button size="sm" variant="secondary" busy={busy} onClick={onUpdate} data-testid="setup-update">{t('setup.update', { name: active.name })}</Button>
            <Button size="sm" variant="ghost" onClick={onStartNaming} data-testid="setup-save-new">{t('setup.saveNew')}</Button>
            <Button size="sm" variant="ghost" onClick={onReset} data-testid="setup-reset">{t('setup.reset')}</Button>
          </span>
        ))}
      </div>
      {warning && <Callout tone="warn" className="setup-warning" testId="setup-warning" role="status">{warning}</Callout>}
    </>
  )
}

// Why the Setup chip is off, as text next to the composer (the chip points at
// it with aria-describedby).
export function SetupReason({ id, text }) {
  return (
    <p className="cx-ready setup-reason" id={id} data-testid="setup-reason">
      <Icon name="info" />
      <span>{text}</span>
    </p>
  )
}
