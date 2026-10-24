/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { pushLayer } from '../../utils/modalLayers'
import { UnknownNotice } from './AddonBits'
import { shortDigest } from './addonState'
import { describeOutcome } from './errors'

// The one dialog of Install and turn on. It asks once, then stays open and
// shows the two steps while they run: the install, then the turn on. It ends
// on the result, or on the reason a step failed with a way to try again.
export default function InstallDialog({ entry, flow, actions, onClose }) {
  const { t } = useTranslation('addons')
  const ref = useRef(null)
  const step = flow ? flow.step : 'ask'
  const running = step === 'install' || step === 'turn-on'
  const closeRef = useRef(onClose)
  closeRef.current = onClose

  useEffect(() => {
    if (!entry) return undefined
    const opener = document.activeElement
    const layer = pushLayer()
    const dialog = ref.current
    dialog?.querySelector('button:not([disabled])')?.focus()
    const onKey = (e) => {
      if (!layer.isTop()) return
      if (e.key === 'Escape') {
        if (dialog?.dataset.running === 'true') return
        e.preventDefault(); closeRef.current(); return
      }
      if (e.key !== 'Tab' || !dialog) return
      const items = Array.from(dialog.querySelectorAll('button:not([disabled]), a[href]'))
      if (items.length === 0) return
      const first = items[0]
      const last = items[items.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      layer.pop()
      if (opener && opener !== document.body && document.contains(opener)) opener.focus?.()
    }
  }, [entry])

  if (!entry) return null
  const start = () => actions.installAndTurnOn(entry)
  const retry = () => actions.installAndTurnOn(entry, { skipInstall: flow.failedStep === 'turn-on' })
  const stepState = (id) => {
    if (step === 'ask') return 'pending'
    if (step === 'done') return 'done'
    if (id === 'install') return step === 'install' ? 'active' : step === 'failed' && flow.failedStep === 'install' ? 'failed' : 'done'
    return step === 'turn-on' ? 'active' : step === 'failed' && flow.failedStep === 'turn-on' ? 'failed' : 'pending'
  }
  const reason = step === 'failed' ? describeOutcome(flow.outcome, t, flow.error) : ''
  return (
    <div className="confirm-dialog-backdrop" onClick={running ? undefined : onClose}>
      <div ref={ref} className="confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="install-dialog-title" data-testid="install-dialog" data-running={running ? 'true' : 'false'} data-step={step} onClick={(e) => e.stopPropagation()}>
        <div className="confirm-dialog-header">
          <span id="install-dialog-title" className="confirm-dialog-title">{t('flow.confirmTitle', { name: entry.title })}</span>
        </div>
        <div className="confirm-dialog-body ad-confirm">
          {step === 'ask' && (
            <>
              <p>{t('flow.confirmBody', { version: entry.version, library: entry.sourceId })}</p>
              <dl className="dk-kv">
                <dt>{t('trust.digest')}</dt><dd className="dk-mono">{shortDigest(entry.digest)}</dd>
                <dt>{t('trust.verification')}</dt><dd>{entry.policy.requireSignature ? t('trust.signatureRequired') : t('trust.digestOnly')}</dd>
              </dl>
            </>
          )}
          {step !== 'ask' && (
            <ol className="ad-steps ad-steps--stack" data-testid="flow-steps" aria-label={t('install.stepsLabel')}>
              {['install', 'turn-on'].map(id => {
                const state = stepState(id)
                return (
                  <li key={id} className="ad-steps__item" data-step={id} data-state={state}>
                    <Icon name={state === 'done' ? 'check-circle' : state === 'failed' ? 'alert-circle' : state === 'active' ? 'spinner' : 'circle'} spin={state === 'active'} />
                    <span>{t(`flow.step.${id}`)}</span>
                    <span className="dk-sr-only">{t(`install.state.${state}`)}</span>
                  </li>
                )
              })}
            </ol>
          )}
          {step === 'done' && (
            <p className="ad-entry__note" data-tone="ok" role="status">
              {t('browse.installedOn', { name: entry.title })}
              {entry.ui && entry.ui.mainPage && <> <Link to="/app/addons">{t('flow.seeInList')}</Link></>}
            </p>
          )}
          {step === 'failed' && (
            <p className="ad-entry__note" data-tone="error" role="alert" data-testid="install-dialog-failed">
              {flow.failedStep === 'install' ? t('flow.installFailed', { name: entry.title }) : t('flow.turnOnFailed', { name: entry.title })}
              {reason ? ` ${reason}` : ''}
            </p>
          )}
          {step === 'failed' && actions.unknown[entry.id] && <UnknownNotice addonId={entry.id} actions={actions} />}
        </div>
        <div className="confirm-dialog-actions">
          {step === 'ask' && <button type="button" className="btn btn-secondary btn-sm" onClick={onClose}>{t('catalog.cancel')}</button>}
          {step === 'ask' && <button type="button" className="btn btn-primary btn-sm" onClick={start}>{t('flow.installAndTurnOn')}</button>}
          {running && <button type="button" className="btn btn-primary btn-sm" disabled>{step === 'install' ? t('flow.installing', { name: entry.title }) : t('flow.turningOn', { name: entry.title })}</button>}
          {step === 'failed' && <button type="button" className="btn btn-secondary btn-sm" onClick={onClose}>{t('catalog.close')}</button>}
          {step === 'failed' && !actions.unknown[entry.id] && <button type="button" className="btn btn-primary btn-sm" onClick={retry}>{t('catalog.retry')}</button>}
          {step === 'done' && <button type="button" className="btn btn-primary btn-sm" onClick={onClose}>{t('catalog.close')}</button>}
        </div>
      </div>
    </div>
  )
}
