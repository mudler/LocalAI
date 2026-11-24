/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useRef, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import CommandBlock from '../swarm/CommandBlock'
import Icon from '../Icon'
import { Button, Callout } from '../ui'
import { p2pRelaysApi } from '../../utils/api'
import { CHECK_STEPS, canAdd, checkOutcome, isRelayAddr, relayCommands, relayPeerID, shortID } from './p2pSettingsModel'

const MARK = { ok: 'check', failed: 'close' }

function Steps({ outcome }) {
  const { t } = useTranslation('p2psettings')
  return (
    <ol className="p2ps-steps" data-testid="relay-check-steps">
      {CHECK_STEPS.map((step, i) => {
        const state = outcome.steps[step]
        const detail = outcome.details[step]
        return (
          <li key={step} className="p2ps-steps__item" data-state={state} data-step={step}>
            <span className="p2ps-steps__mark" aria-hidden="true">{MARK[state] ? <Icon name={MARK[state]} /> : i + 1}</span>
            <span className="p2ps-steps__text">
              <span>{t(`run.steps.${step}.${state}`)}</span>
              {detail && state === 'failed' && <span className="p2ps-steps__detail">{detail}</span>}
            </span>
          </li>
        )
      })}
    </ol>
  )
}

// The result of a check, in one Callout: what works, or what to fix.
function Result({ outcome, addr }) {
  const { t } = useTranslation('p2psettings')
  const k = outcome.kind
  if (k === 'ok') return <Callout tone="ok" title={t('run.result.ok.title')} testId="relay-check-result" data-kind={k}>{t('run.result.ok.body')}</Callout>
  if (k === 'open') return <Callout tone="warn" title={t('run.result.open.title')} testId="relay-check-result" data-kind={k}>{t('run.result.open.body')}</Callout>
  if (k === 'wrong_peer') {
    return (
      <Callout tone="error" role="alert" title={t('run.result.wrong_peer.title')} testId="relay-check-result" data-kind={k}>
        {t('run.result.wrong_peer.body', { actual: shortID(outcome.peer?.actual), expected: shortID(outcome.peer?.expected || relayPeerID(addr)) })}
      </Callout>
    )
  }
  return <Callout tone={k === 'unclaimed' ? 'warn' : 'error'} role={k === 'unclaimed' ? undefined : 'alert'} title={t(`run.result.${k}.title`)} testId="relay-check-result" data-kind={k}>{t(`run.result.${k}.body`)}</Callout>
}

// Run a relay: the commands that start a gated relay for this server, then
// the relay address, a check, and Add relay. The server stores a relay when
// it checks it, so a relay checked here and not added is removed again when
// the dialog closes: nothing is kept until the person adds it.
export default function RunRelayDialog({ peerID, onClose, onAdded, addToast }) {
  const { t } = useTranslation('p2psettings')
  const commands = relayCommands(peerID)
  const [addr, setAddr] = useState('')
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState('')
  const [outcome, setOutcome] = useState(null)
  const [claimCode, setClaimCode] = useState('')
  const [claiming, setClaiming] = useState(false)
  // The relay this dialog stored for the check, and whether it was kept.
  const pending = useRef(null)

  const discard = async () => {
    const id = pending.current
    pending.current = null
    if (!id) return
    try { await p2pRelaysApi.remove(id) } catch { /* the list shows it if it stays */ }
  }

  const runCheck = async (id) => {
    const check = await p2pRelaysApi.check(id)
    setOutcome(checkOutcome(check))
  }

  const check = async () => {
    const value = addr.trim()
    if (!isRelayAddr(value)) { setError(t('run.addrInvalid')); return }
    setError(''); setOutcome(null); setChecking(true)
    try {
      await discard()
      const relay = await p2pRelaysApi.add(value)
      pending.current = relay.id
      await runCheck(relay.id)
    } catch (err) {
      setError(err.message)
    } finally {
      setChecking(false)
    }
  }

  const claim = async () => {
    if (!pending.current || !claimCode.trim()) return
    setClaiming(true)
    try {
      await p2pRelaysApi.claim(pending.current, claimCode.trim())
      await runCheck(pending.current)
      setClaimCode('')
    } catch (err) {
      addToast?.(t('run.claimFailed', { error: err.message }), 'error')
    } finally {
      setClaiming(false)
    }
  }

  const close = async () => {
    await discard()
    onClose()
  }

  const add = () => {
    pending.current = null
    addToast?.(t('run.added'), 'success')
    onAdded()
  }

  const ready = outcome && canAdd(outcome.kind)
  const badAddr = Boolean(error) || outcome?.kind === 'wrong_peer' || outcome?.kind === 'unreachable'

  return (
    <Dialog
      wide title={<><Icon name="server" /> {t('run.title')}</>} description={t('run.lede')}
      onClose={close} pending={checking || claiming} closeLabel={t('actions.close')} testId="run-relay-dialog" labelId="run-relay-title"
      foot={(
        <>
          <span className="p2ps-foot-note">{ready ? t('run.ready') : t('run.nothingSaved')}</span>
          <Button onClick={close} disabled={checking}>{t('actions.cancel')}</Button>
          <Button variant="primary" disabled={!ready || checking} onClick={add} data-testid="run-relay-add">{t('run.add')}</Button>
        </>
      )}
    >
      <div className="p2ps-section">
        <p className="p2ps-step-title">{t('run.step1')}</p>
        <CommandBlock command={commands.docker} label={t('run.dockerLabel')} addToast={addToast} />
        <p className="dk-hint">{t('run.orBinary')}</p>
        <CommandBlock command={commands.binary} label={t('run.binaryLabel')} addToast={addToast} />
        <p className="dk-hint">
          <Trans t={t} i18nKey="run.explain" components={{ b: <strong />, code: <code /> }} />
        </p>

        <label className="p2ps-step-title" htmlFor="run-relay-addr">{t('run.step2')}</label>
        <div className="p2ps-addr">
          <input
            id="run-relay-addr" className="dk-input dk-input--mono" value={addr} spellCheck={false}
            placeholder="/dns4/relay.example.org/udp/4001/quic-v1/p2p/12D3KooW…"
            aria-invalid={badAddr || undefined} aria-describedby="run-relay-addr-hint"
            onChange={(e) => { setAddr(e.target.value); setError('') }}
            onKeyDown={(e) => { if (e.key === 'Enter') check() }}
          />
          <Button variant={outcome ? 'secondary' : 'primary'} busy={checking} disabled={!addr.trim() || checking} onClick={check} data-testid="run-relay-check">{t('run.check')}</Button>
        </div>
        <p className="dk-hint" id="run-relay-addr-hint">{error || t('run.addrHint')}</p>

        {outcome && <Steps outcome={outcome} />}
        {outcome?.kind === 'unclaimed' && (
          <div className="p2ps-claim">
            <div className="dk-field">
              <label className="dk-label" htmlFor="run-relay-claim">{t('run.claimLabel')}</label>
              <input id="run-relay-claim" className="dk-input dk-input--mono" value={claimCode} placeholder="A972-F9B0-048A" onChange={(e) => setClaimCode(e.target.value)} />
            </div>
            <Button busy={claiming} disabled={!claimCode.trim() || claiming} onClick={claim} data-testid="run-relay-claim">{t('run.claim')}</Button>
          </div>
        )}
        {outcome && <Result outcome={outcome} addr={addr} />}
      </div>
    </Dialog>
  )
}
