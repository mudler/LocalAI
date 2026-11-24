/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { clusterApi } from '../../utils/api'
import { busState, natsOutOfReach, natsState } from '../../utils/clusterSettings'
import {
  CONNECTIONS, WEIGHT_RANGE, defaultWeights, preferenceOrder, resetPayload, tieWinner, weightErrors, weightsInForce, weightsPayload,
} from '../../utils/connectionWeights'
import { Button, Callout, Slider, StatusChip, Switch } from '../ui'

// One section of the cluster settings: a heading, a sentence that says what it
// does, and its controls. The sections are the same wherever the form is drawn.
function Section({ id, title, note, aside, children }) {
  const headId = useId()
  return (
    <section className="cl-section" aria-labelledby={headId} data-testid={`cluster-section-${id}`}>
      <div className="cl-section__head">
        <h3 className="cl-section__title" id={headId}>{title}</h3>
        {aside}
      </div>
      {note && <p className="cl-note">{note}</p>}
      {children}
    </section>
  )
}

// The broadcast bus of this frontend and the frontends that share the cluster.
export function BusSection({ settings }) {
  const { t } = useTranslation('cluster')
  const bus = busState(settings.bus)
  const replicas = settings.replicas || []
  return (
    <Section id="bus" title={t('bus.title')} note={t('bus.note')} aside={<StatusChip tone={bus.tone} dot testId="cluster-bus">{t(`bus.state.${bus.key}`)}</StatusChip>}>
      {natsOutOfReach(settings.bus) && (
        <Callout tone="warn" role="status" testId="cluster-bus-note">{t('bus.unreachable')}</Callout>
      )}
      {replicas.length === 0
        ? <p className="cl-note">{t('bus.noReplica')}</p>
        : (
          <ul className="cl-list" aria-label={t('bus.replicas')}>
            {replicas.map(replica => (
              <li key={replica.id} className="cl-list__row" data-testid="cluster-replica">
                <span className="cl-list__name dk-mono">{replica.id}</span>
                <span className="cl-list__meta">{replica.version || t('bus.unknownVersion')}</span>
                <StatusChip tone={bus.tone} dot>{t(`bus.publishing.${bus.key}`)}</StatusChip>
              </li>
            ))}
          </ul>
        )}
      <p className="cl-note">{t('bus.workers')}</p>
    </Section>
  )
}

// The NATS address of the frontends and the one workers are told to dial.
export function NatsSection({ settings, onSaved }) {
  const { t } = useTranslation('cluster')
  const id = useId()
  const [natsUrl, setNatsUrl] = useState(settings.nats_url || '')
  const [workerUrl, setWorkerUrl] = useState(settings.nats_worker_url || '')
  const [saving, setSaving] = useState(false)
  const [result, setResult] = useState(null)
  const touched = useRef(false)
  const state = natsState(settings)

  // Follow the stored value until the admin starts typing.
  useEffect(() => {
    if (touched.current) return
    setNatsUrl(settings.nats_url || '')
    setWorkerUrl(settings.nats_worker_url || '')
  }, [settings.nats_url, settings.nats_worker_url])

  const save = async event => {
    event.preventDefault()
    setSaving(true)
    setResult(null)
    try {
      const answer = await clusterApi.saveSettings({ nats_url: natsUrl.trim(), nats_worker_url: workerUrl.trim() })
      touched.current = false
      setResult({ ok: true, text: t(answer?.nats?.reachable ? 'nats.savedReachable' : 'nats.saved') })
      onSaved()
    } catch (error) {
      setResult({ ok: false, text: error.message || t('nats.failed') })
    } finally {
      setSaving(false)
    }
  }

  const tone = { connected: 'ok', unreachable: 'warn', off: 'idle' }[state]
  return (
    <Section id="nats" title={t('nats.title')} note={t('nats.note')} aside={<StatusChip tone={tone} dot testId="cluster-nats-state">{t(`nats.state.${state}`)}</StatusChip>}>
      <form className="cl-form" onSubmit={save} aria-label={t('nats.title')}>
        <div className="cl-field">
          <label className="cl-label" htmlFor={`${id}-url`}>{t('nats.url')}</label>
          <input
            id={`${id}-url`} className="dk-input dk-input--mono" placeholder="nats://nats:4222" value={natsUrl} autoComplete="off" spellCheck={false}
            onChange={event => { touched.current = true; setNatsUrl(event.target.value) }}
          />
          <span className="cl-hint">{t('nats.urlHint')}</span>
        </div>
        <div className="cl-field">
          <label className="cl-label" htmlFor={`${id}-worker`}>{t('nats.workerUrl')}</label>
          <input
            id={`${id}-worker`} className="dk-input dk-input--mono" placeholder="nats://nats.internal:4222" value={workerUrl} autoComplete="off" spellCheck={false}
            onChange={event => { touched.current = true; setWorkerUrl(event.target.value) }}
          />
          <span className="cl-hint">{t('nats.workerUrlHint')}</span>
        </div>
        <div className="cl-actions">
          <Button type="submit" size="sm" busy={saving} data-testid="cluster-nats-save">{t('nats.save')}</Button>
          {result && <p className={`cl-result${result.ok ? '' : ' cl-result--error'}`} role="status" data-testid="cluster-nats-result">{result.text}</p>}
        </div>
      </form>
    </Section>
  )
}

function toDraft(weights) {
  return Object.fromEntries(CONNECTIONS.map(({ key }) => [key, String(weights[key])]))
}

// How much the router prefers a worker that a frontend reaches over NATS,
// WebSocket or P2P when two workers fit a request equally well. A weight never
// overrides fit. A save reaches every frontend within seconds.
export function WeightsSection({ settings, onSaved }) {
  const { t } = useTranslation('cluster')
  const current = weightsInForce(settings)
  const defaults = defaultWeights(settings)
  const [draft, setDraft] = useState(() => toDraft(current))
  const [saving, setSaving] = useState(false)
  const [result, setResult] = useState(null)
  const touched = useRef(false)
  const currentKey = CONNECTIONS.map(({ key }) => current[key]).join(',')

  // Follow the stored values until the admin starts moving a slider.
  useEffect(() => {
    if (touched.current) return
    setDraft(toDraft(weightsInForce(settings)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentKey])

  const errors = weightErrors(draft)
  const payload = weightsPayload(draft, current)
  const reset = resetPayload(current, defaults)

  const send = async (body, okKey) => {
    setSaving(true)
    setResult(null)
    try {
      await clusterApi.saveSettings(body)
      touched.current = false
      setResult({ ok: true, text: t(okKey) })
      onSaved()
    } catch (error) {
      setResult({ ok: false, text: error.message || t('weights.failed') })
    } finally {
      setSaving(false)
    }
  }
  const useDefaults = () => {
    touched.current = false
    setDraft(toDraft(defaults))
    if (reset) send(reset, 'weights.defaultsBack')
  }
  const set = (key, text) => {
    touched.current = true
    // The slider's reset puts the default back.
    setDraft(previous => ({ ...previous, [key]: text === '' ? String(defaults[key]) : text }))
  }

  const numbers = Object.fromEntries(CONNECTIONS.map(({ key }) => [key, Number(draft[key])]))
  const valid = Object.keys(errors).length === 0
  const order = preferenceOrder(numbers)
  const name = key => CONNECTIONS.find(c => c.key === key).label
  const sentence = []
  for (let i = 0; i < order.length - 1; i += 1) {
    const win = tieWinner(numbers, order[i], order[i + 1])
    sentence.push(win ? t('weights.wins', { winner: name(win[0]), loser: name(win[1]) }) : t('weights.ties', { a: name(order[i]), b: name(order[i + 1]) }))
  }

  return (
    <Section id="weights" title={t('weights.title')} note={t('weights.note')}>
      <div className="cl-weights">
        {CONNECTIONS.map(({ key, label }) => (
          <div className="cl-weight" key={key} data-testid={`cluster-weight-${key}`}>
            <div className="cl-weight__head">
              <span className="cl-label">{label}</span>
              <span className="cl-hint">{t(`weights.about.${key}`)}</span>
            </div>
            <Slider
              range={WEIGHT_RANGE}
              value={draft[key]}
              onChange={text => set(key, text)}
              label={t('weights.sliderLabel', { connection: label })}
              placeholder={String(defaults[key])}
              resetLabel={t('weights.resetOne', { value: defaults[key] })}
              invalid={Boolean(errors[key])}
              testId={`cluster-weight-${key}-slider`}
            />
            <span className="cl-hint">{errors[key] ? <span className="cl-error" role="alert" data-testid={`cluster-weight-error-${key}`}>{t(`weights.error.${errors[key]}`)}</span> : t('weights.default', { value: defaults[key] })}</span>
          </div>
        ))}
      </div>
      {valid && <p className="cl-live" role="status" data-testid="cluster-weights-sentence">{t('weights.live', { sentence: sentence.join(', ') })}</p>}
      <div className="cl-actions">
        <Button size="sm" busy={saving} disabled={!payload} onClick={() => payload && send(payload, 'weights.saved')} data-testid="cluster-weights-apply">{t('weights.apply')}</Button>
        <Button size="sm" variant="ghost" disabled={saving || !reset} onClick={useDefaults} data-testid="cluster-weights-defaults">{t('weights.useDefaults')}</Button>
        {result && <p className={`cl-result${result.ok ? '' : ' cl-result--error'}`} role="status" data-testid="cluster-weights-result">{result.text}</p>}
      </div>
    </Section>
  )
}

// Joining: whether a worker that joins with a token still waits for an admin.
// A switch applies at once.
export function JoiningSection({ settings, onSaved }) {
  const { t } = useTranslation('cluster')
  const labelId = useId()
  const noteId = useId()
  const [pending, setPending] = useState(null)
  const [error, setError] = useState('')
  const on = pending ?? Boolean(settings.join_auto_approve)

  const change = async next => {
    setPending(next)
    setError('')
    try {
      await clusterApi.saveSettings({ join_auto_approve: next })
      await onSaved()
    } catch (err) {
      setError(err.message || t('joining.failed'))
    } finally {
      setPending(null)
    }
  }

  return (
    <Section id="joining" title={t('joining.title')} note={t('joining.note')}>
      <div className="cl-switch">
        <div className="cl-switch__words">
          <span className="cl-label" id={labelId}>{t('joining.autoApprove')}</span>
          <span className="cl-hint" id={noteId}>{t('joining.autoApproveHint')}</span>
        </div>
        <Switch checked={on} onChange={change} labelledBy={labelId} describedBy={noteId} disabled={pending !== null} testId="cluster-auto-approve" />
      </div>
      {on && <Callout tone="warn" testId="cluster-auto-approve-warning">{t('joining.autoApproveOn')}</Callout>}
      {error && <p className="cl-result cl-result--error" role="alert" data-testid="cluster-joining-error">{error}</p>}
    </Section>
  )
}
