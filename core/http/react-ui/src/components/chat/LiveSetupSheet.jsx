/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import SideSheet from '../SideSheet'
import Icon from '../Icon'
import { Button, Callout } from '../ui'
import { modelsApi } from '../../utils/api'
import { RECOMMENDED } from '../../utils/liveModels'
import { useOperations } from '../../hooks/useOperations'
import { useAuth } from '../../context/AuthContext'
import './live.css'

const PARTS = ['stt', 'tts']
const POLL_MS = 2000

// The Live setup sheet. Live needs one transcription model and one speech
// model. The sheet shows each part, installs a missing one from the gallery
// and, when both are there, offers Start Live. Start Live is a button because
// the browser lets the page play audio only after a click.
//
//   models      { stt, tts }: the installed model of each part, '' when missing
//   onClose     closes the sheet
//   onInstalled reads the model lists again (the hook that holds them)
//   onStart     starts Live; it runs inside the click on Start Live
//   slowMs      how long an install may stay out of the operations list with
//               no model to show before the sheet calls it lost
export default function LiveSetupSheet({ models, onClose, onInstalled, onStart, slowMs = 10000 }) {
  const { t } = useTranslation('chat')
  const { isAdmin } = useAuth()
  const { operations, dismissFailedOp } = useOperations()
  // Parts whose install request this sheet sent and that are not installed yet.
  const [asked, setAsked] = useState(() => ({}))
  // Parts whose install request was refused: { stt: 'message' }.
  const [refused, setRefused] = useState(() => ({}))
  const seen = useRef({})

  const target = (part) => RECOMMENDED[part][0]
  const opFor = (part) => operations.find(o => RECOMMENDED[part].includes(o.name) && !o.isBackend && !o.isDeletion)
  const ready = PARTS.every(p => !!models?.[p])

  // The lists may be stale when the sheet opens (an install can have ended
  // while it was closed): read them again at once.
  useEffect(() => { onInstalled?.() }, [onInstalled])

  // An install that has left the operations list is over, or nearly: read the
  // lists again. If the model still does not show up after slowMs, the install
  // is called lost and the person can retry.
  const lost = PARTS.map(p => (asked[p] && !models?.[p] && !opFor(p) ? p : '')).join(',')
  useEffect(() => {
    for (const part of PARTS) {
      if (opFor(part)) seen.current[part] = true
      else if (asked[part] && seen.current[part]) {
        seen.current[part] = false
        onInstalled?.()
      }
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [operations, asked])
  useEffect(() => {
    const parts = lost.split(',').filter(Boolean)
    if (parts.length === 0) return undefined
    const id = setTimeout(() => {
      setAsked(a => { const n = { ...a }; for (const p of parts) n[p] = false; return n })
      setRefused(r => { const n = { ...r }; for (const p of parts) n[p] = t('live.setup.notShown'); return n })
    }, slowMs)
    return () => clearTimeout(id)
  }, [lost, slowMs, t])

  // While an install runs the model lists are read again from time to time, so
  // a model that lands appears with no reload.
  const waiting = PARTS.some(p => !models?.[p] && (asked[p] || opFor(p)))
  useEffect(() => {
    if (!waiting) return undefined
    const id = setInterval(() => onInstalled?.(), POLL_MS)
    return () => clearInterval(id)
  }, [waiting, onInstalled])

  const install = useCallback(async (part, failedOp) => {
    seen.current[part] = false
    setRefused(r => ({ ...r, [part]: '' }))
    setAsked(a => ({ ...a, [part]: true }))
    try {
      if (failedOp?.jobID) await dismissFailedOp(failedOp.jobID)
      await modelsApi.install(target(part))
    } catch (err) {
      setAsked(a => ({ ...a, [part]: false }))
      setRefused(r => ({ ...r, [part]: err?.message || t('live.setup.failedGeneric') }))
    }
  }, [dismissFailedOp, t])

  const row = (part) => {
    const id = target(part)
    const installed = models?.[part]
    const op = opFor(part)
    const failed = op?.error ? op.error : ''
    const error = refused[part] || failed
    const running = !installed && !error && (asked[part] || !!op)
    const pct = Math.round(op?.progress || 0)
    return (
      <li key={part} className="live-setup__item" data-testid={`live-setup-${part}`}>
        <div className="live-setup__line">
          <div className="live-setup__info">
            <strong className="live-setup__part">{t(`live.setup.part.${part}`)}</strong>
            <code className="live-setup__name" title={id}>{installed || id}</code>
            <span className="live-setup__hint">{t(`live.setup.hint.${part}`)}</span>
          </div>
          {installed && (
            <span className="live-setup__done" data-testid={`live-setup-done-${part}`}>
              <Icon name="check-circle" /> {t('live.setup.installed')}
            </span>
          )}
          {!installed && isAdmin && !error && (
            <Button
              variant={part === 'stt' ? 'primary' : 'secondary'}
              size="sm"
              icon="download"
              busy={running}
              disabled={running}
              data-testid={`live-setup-install-${part}`}
              aria-label={t('live.setup.installLabel', { model: id })}
              onClick={() => install(part)}
            >
              {running ? t('live.setup.installing') : t('live.setup.install')}
            </Button>
          )}
          {!installed && isAdmin && error && (
            <Button
              variant="secondary"
              size="sm"
              icon="refresh"
              data-testid={`live-setup-retry-${part}`}
              aria-label={t('live.setup.retryLabel', { model: id })}
              onClick={() => install(part, failed ? op : null)}
            >
              {t('live.retry')}
            </Button>
          )}
        </div>
        {running && op && (
          <progress
            className="live-setup__bar"
            max={100}
            value={pct}
            aria-label={t('live.setup.progress', { model: id })}
            aria-valuenow={pct}
          />
        )}
        {!installed && isAdmin && error && (
          <Callout tone="error" role="alert" title={t('live.setup.failed', { model: id })}>
            <span className="live-setup__error">{error}</span>
          </Callout>
        )}
      </li>
    )
  }

  // Polite text for a screen reader: what is installing, in words.
  const doing = PARTS.filter(p => !models?.[p] && (asked[p] || opFor(p)) && !refused[p] && !opFor(p)?.error)
    .map(p => `${target(p)} ${opFor(p) ? `${Math.round(opFor(p).progress || 0)}%` : ''}`.trim())
    .join(', ')

  return (
    <SideSheet
      title={t('live.setup.title')}
      description={t('live.setup.description')}
      onClose={onClose}
      closeLabel={t('live.setup.close')}
      testId="live-setup"
      labelId="live-setup-title"
      footer={ready ? (
        <Button variant="primary" icon="waveform" data-testid="live-start" onClick={onStart}>{t('live.setup.start')}</Button>
      ) : (
        <Button variant="secondary" onClick={onClose}>{t('live.setup.close')}</Button>
      )}
    >
      <ul className="live-setup__list">{PARTS.map(row)}</ul>
      {!isAdmin && !ready && (
        <Callout tone="info" title={t('live.setup.adminTitle')}>{t('live.setup.adminNote')}</Callout>
      )}
      <p className="live-setup__status" aria-live="polite">
        {ready ? t('live.setup.ready') : (doing ? t('live.setup.working', { models: doing }) : '')}
      </p>
    </SideSheet>
  )
}
