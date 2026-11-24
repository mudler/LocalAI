/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useProviderSlot } from '../hooks/useProviderSlot'
import { PROVIDER_BUILTIN, forgettable, pickable, providerFacts, providerFailure } from '../utils/providerSlots'
import ConfirmDialog from './ConfirmDialog'
import { Button, LoadError, Notice, SkeletonRows } from './ui'
import './memorysettings.css'

// The choice of provider for one slot, drawn from what the server lists: the
// built-in provider, every add-on that declares the slot, and Off when the slot
// allows it. It knows no slot. The words of a built-in provider and of Off come
// from the locale files by slot id, with the server's English as the fallback;
// an add-on brings its own title and description.
//
//   slot       the slot id
//   version    changes when something else changed the slot, to read it again
//   onChange   called after a pick was stored
//   addToast   shows a failure
//   forget     optional: async () => { done, proposal }. It asks the provider that
//              serves to forget what it keeps. The action is offered only while
//              an add-on serves and declares the forget role; the built-in
//              provider has none.
export default function ProviderPicker({ slot, version, onChange, addToast, forget }) {
  const { t } = useTranslation('chats')
  const { view, error, saving, reload, pick } = useProviderSlot(slot, version)
  const titleId = useId()
  const testId = `provider-picker-${slot}`
  const [asking, setAsking] = useState(false)
  const [forgetting, setForgetting] = useState(false)

  if (error && !view) return <LoadError title={t('providerPicker.loadError')} error={error} onRetry={reload} testId={`${testId}-error`} />
  if (!view) return <SkeletonRows count={2} />

  const failure = providerFailure(view)
  const serving = forget ? forgettable(view) : null
  const doForget = async () => {
    setForgetting(true)
    try {
      const r = await forget()
      addToast?.(r?.done === false ? t('providerPicker.forget.waiting') : t('providerPicker.forget.done', { name: serving.title }), 'success')
    } catch (e) {
      addToast?.(t('providerPicker.forget.failed', { message: e.message }), 'error')
    } finally {
      setForgetting(false)
      setAsking(false)
    }
  }
  const choose = async (id) => {
    if (!pickable(view, id)) return
    if (await pick(id)) onChange?.(id)
    else addToast?.(t('providerPicker.saveFailed'), 'error')
  }
  const words = (p) => {
    if (p.kind === 'builtin') return { title: t(`providerPicker.slots.${slot}.builtin`, { defaultValue: p.title }), body: t(`providerPicker.slots.${slot}.builtinBody`, { defaultValue: p.description }) }
    if (p.kind === 'off') return { title: t('providerPicker.off'), body: t(`providerPicker.slots.${slot}.offBody`, { defaultValue: p.description }) }
    return { title: p.title, body: p.description }
  }

  return (
    <section className="ms-card" aria-labelledby={titleId} data-testid={testId} data-active={view.resolution.active}>
      <h3 className="ms-h" id={titleId}>{t('providerPicker.title')}</h3>
      <p className="ms-p">{t('providerPicker.lede')}</p>
      {failure && (
        <Notice
          severity="warn"
          title={t('providerPicker.failedTitle', { name: failure.title })}
          reason={t('providerPicker.failedReason')}
          fix={{ label: t('providerPicker.useBuiltin'), onClick: () => choose(PROVIDER_BUILTIN), busy: saving, testId: `${testId}-use-builtin` }}
          testId={`${testId}-failed`}
        />
      )}
      {view.health?.notice && (
        <Notice
          severity="warn"
          title={t('providerPicker.healthTitle')}
          reason={view.health.notice}
          testId={`${testId}-health`}
        />
      )}
      <div className="ms-levels" role="radiogroup" aria-labelledby={titleId}>
        {view.providers.map((p) => {
          const w = words(p)
          return (
            <label key={p.id} className={`ms-level${p.selected ? ' ms-level--on' : ''}`} data-testid={`${testId}-${p.id}`}>
              <input type="radio" name={`provider-${slot}`} value={p.id} checked={p.selected} disabled={saving || (!p.available && !p.selected)} onChange={() => choose(p.id)} />
              <span className="ms-level__text">
                <span className="ms-level__title">
                  {w.title}
                  {p.kind === 'addon' && !p.available && <span className="ms-tag">{t('providerPicker.unavailable')}</span>}
                  {providerFacts(p).map((f) => <span key={f} className="ms-tag" data-testid={`${testId}-${p.id}-${f}`}>{t(`providerPicker.facts.${f}`)}</span>)}
                </span>
                {w.body && <span className="ms-level__body">{w.body}</span>}
              </span>
            </label>
          )
        })}
      </div>
      {error && <p className="ms-p ms-p--warn" role="alert" data-testid={`${testId}-save-error`}>{error.message}</p>}
      {serving && (
        <div className="ms-forget" data-testid={`${testId}-forget`}>
          <p className="ms-p">{t('providerPicker.forget.lede', { name: serving.title })}</p>
          <Button variant="danger" size="sm" icon="trash" disabled={forgetting} data-testid={`${testId}-forget-button`} onClick={() => setAsking(true)}>{t('providerPicker.forget.action')}</Button>
          <ConfirmDialog
            open={asking}
            danger
            pending={forgetting}
            title={t('providerPicker.forget.confirmTitle', { name: serving.title })}
            message={t(serving.undo === 'none' ? 'providerPicker.forget.confirmBodyFinal' : 'providerPicker.forget.confirmBody', { name: serving.title })}
            confirmLabel={t('providerPicker.forget.confirm')}
            pendingLabel={t('providerPicker.forget.pending')}
            onConfirm={doForget}
            onCancel={() => setAsking(false)}
          />
        </div>
      )}
    </section>
  )
}
