/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Button from '../ui/Button'
import { pushLayer } from '../../utils/modalLayers'
import { EntryAdds, UnknownNotice } from './AddonBits'
import { shortDigest, skillShadowNotes } from './addonState'
import { skillInstallNoticeKey } from '../../utils/chatSkills'
import { describeOutcome } from './errors'
import Callout from '../ui/Callout'
import HostAccessList from './HostAccessList'
import { useHostPreview } from './useHostPreview'
import { approvalBody, hostErrorKey, installBlock, needsStep } from './hostGrantsState'
import { fileAction, fileGate } from './fileInstall'

// The built-in chat tools that the entry replaces and that an installed add-on
// already replaces: only one of them can serve the tool, so the dialog says so
// before the install (which is still allowed).
export function shadowConflicts(entry, installed) {
  const want = Array.isArray(entry?.shadows) ? entry.shadows : []
  const out = []
  for (const tool of want) {
    const other = Object.values(installed || {}).find(a => a && a.id !== entry.id && Array.isArray(a.shadows) && a.shadows.includes(tool))
    if (other) out.push({ tool, name: other.title || other.id })
  }
  return out
}

// The one dialog of Install and turn on. It asks once, then stays open and
// shows the two steps while they run: the install, then the turn on. It ends
// on the result, or on the reason a step failed with a way to try again.
export default function InstallDialog({ entry, flow, actions, onClose, installed = {} }) {
  const { t } = useTranslation('addons')
  const ref = useRef(null)
  const step = flow ? flow.step : 'ask'
  const running = step === 'install' || step === 'turn-on'
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  // What the verified package asks of this server. Read while the dialog asks;
  // the approval it builds is checked again by the owner against the package it
  // really stages.
  const host = useHostPreview(entry, { enabled: Boolean(entry) && step === 'ask' })
  const approval = useRef(null)
  // An install from a file: a package nothing signs. The person sees the file's
  // own facts and, for an older version, ticks a box first.
  const local = entry ? entry.local : undefined
  const [downgrade, setDowngrade] = useState(false)

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
  const showHost = host.status === 'ready' && needsStep(host.preview)
  const block = showHost ? installBlock(host.preview) : null
  const gate = local ? fileGate(local, { downgradeTicked: downgrade, locked: Boolean(host.preview && host.preview.locked) }) : { allowed: true }
  const start = () => {
    if (local) {
      // A file installs only with the approval of this dialog, with or without
      // host operations: the owner refuses a file install that has none.
      approval.current = approvalBody(host.preview, host.ticked, { action: fileAction(local), sourceId: entry.sourceId, addonId: entry.id, version: entry.version, downgrade: local.relation === 'downgrade' && downgrade })
      return actions.installAndTurnOn(entry, { approval: approval.current, turnOn: !local.installed })
    }
    approval.current = showHost
      ? approvalBody(host.preview, host.ticked, { action: 'install', sourceId: entry.sourceId, addonId: entry.id, version: entry.version })
      : null
    return actions.installAndTurnOn(entry, approval.current ? { approval: approval.current } : {})
  }
  const retry = () => actions.installAndTurnOn(entry, { skipInstall: flow.failedStep === 'turn-on', approval: approval.current, turnOn: !(local && local.installed) })
  const stepState = (id) => {
    if (step === 'ask') return 'pending'
    if (step === 'done') return 'done'
    if (id === 'install') return step === 'install' ? 'active' : step === 'failed' && flow.failedStep === 'install' ? 'failed' : 'done'
    return step === 'turn-on' ? 'active' : step === 'failed' && flow.failedStep === 'turn-on' ? 'failed' : 'pending'
  }
  const reason = step === 'failed' ? describeOutcome(flow.outcome, t, flow.error) : ''
  return (
    <div className="confirm-dialog-backdrop" onClick={running ? undefined : onClose}>
      <div ref={ref} className="confirm-dialog ad-dialog" role="alertdialog" aria-modal="true" aria-labelledby="install-dialog-title" data-testid="install-dialog" data-running={running ? 'true' : 'false'} data-step={step} onClick={(e) => e.stopPropagation()}>
        <div className="confirm-dialog-header">
          <span id="install-dialog-title" className="confirm-dialog-title">{local ? t(local.installed ? 'file.upgradeTitle' : 'file.installTitle', { name: entry.title }) : t('flow.confirmTitle', { name: entry.title })}</span>
        </div>
        <div className="confirm-dialog-body ad-confirm">
          {step === 'ask' && (
            <>
              {local
                ? <p>{t(local.installed ? 'file.confirmBodyUpgrade' : 'file.confirmBody', { version: local.version, name: local.name })}</p>
                : <p>{t('flow.confirmBody', { version: entry.version, library: entry.sourceId })}</p>}
              {local && (
                <Callout tone="warn" testId="install-unsigned" title={t('file.unsignedTitle')}>
                  {t('file.unsignedNotice')}
                </Callout>
              )}
              {local && host.preview && host.preview.locked && (
                <Callout tone="error" role="alert" testId="install-file-locked">{t(host.preview.lockedReason === 'no_auth' ? 'hostAccess.lockedNoAuth' : 'hostAccess.locked')}</Callout>
              )}
              {local && local.relation === 'upgrade' && (
                <p className="ad-quiet" data-testid="install-file-upgrade">{t('file.relation.upgrade', { installed: local.installedVersion, version: local.version })}</p>
              )}
              {local && local.relation === 'same' && (
                <Callout tone="error" role="alert" testId="install-file-same">{t('file.relation.same', { version: local.version })}</Callout>
              )}
              {local && local.relation === 'downgrade' && (
                <Callout tone="warn" testId="install-file-downgrade" title={t('file.relation.downgradeTitle')}>
                  <p>{t('file.relation.downgrade', { installed: local.installedVersion, version: local.version })}</p>
                  <label>
                    <input type="checkbox" checked={downgrade} onChange={(e) => setDowngrade(e.target.checked)} data-testid="install-file-downgrade-confirm" />
                    {' '}{t('file.relation.downgradeConfirm')}
                  </label>
                </Callout>
              )}
              {!local && <EntryAdds entry={entry} />}
              {shadowConflicts(entry, installed).map(c => (
                <Callout key={c.tool} tone="warn" testId="install-shadow-warning" data-tool={c.tool}>
                  {t('flow.shadowWarning', { tool: c.tool, name: c.name })}
                </Callout>
              ))}
              {skillShadowNotes(entry, installed).map(c => (
                <Callout key={c.skill} tone={c.name ? 'warn' : 'info'} testId="install-skill-warning" data-skill={c.skill}>
                  {t(skillInstallNoticeKey(c), { skill: c.skill, name: c.name })}
                </Callout>
              ))}
              {host.status === 'loading' && <p className="ad-quiet" role="status" data-testid="hostaccess-loading">{t('hostAccess.loading')}</p>}
              {host.status === 'error' && (
                <Callout tone="warn" testId="hostaccess-error" actions={<Button size="sm" icon="refresh" onClick={host.retry}>{t('catalog.retry')}</Button>}>
                  {t('hostAccess.previewFailed')}
                </Callout>
              )}
              {showHost && (
                <section className="ad-hostaccess" aria-labelledby="install-host-title" data-testid="install-host-access">
                  <h3 id="install-host-title" className="ad-hostaccess__title">{t('hostAccess.stepTitle')}</h3>
                  <p>{t('hostAccess.stepIntro', { name: entry.title })}</p>
                  {!local && host.preview.official === false && (
                    <p className="ad-quiet" data-testid="hostaccess-not-official">{t('hostAccess.notOfficial', { library: entry.sourceId })}</p>
                  )}
                  <HostAccessList ops={host.preview.ops} ticked={host.ticked} onTick={host.setTick} locked={host.preview.locked} />
                  {host.preview.locked && <Callout tone="info" testId="hostaccess-locked">{t(host.preview.lockedReason === 'no_auth' ? 'hostAccess.lockedNoAuth' : 'hostAccess.locked')}</Callout>}
                  {block && (
                    <Callout tone="error" role="alert" testId="hostaccess-blocked">
                      {t(block.kind === 'blocked' ? 'hostAccess.cannotInstallBlocked' : 'hostAccess.cannotInstallUnsupported', { ops: block.ops.join(', ') })}
                    </Callout>
                  )}
                  <p className="ad-quiet">{t('hostAccess.stepFooter')}</p>
                </section>
              )}
              {local ? (
                <dl className="dk-kv" data-testid="install-file-facts">
                  <dt>{t('file.facts.file')}</dt><dd className="ad-wrap">{local.name}</dd>
                  <dt>{t('file.facts.sha256')}</dt><dd className="dk-mono ad-wrap" data-testid="install-file-sha">{local.sha256}</dd>
                  <dt>{t('file.facts.addon')}</dt><dd>{local.addonId} {local.version}</dd>
                  <dt>{t('file.facts.platform')}</dt><dd>{local.platform}</dd>
                  <dt>{t('file.facts.api')}</dt><dd>{local.api.min === local.api.max ? local.api.min : `${local.api.min} - ${local.api.max}`}</dd>
                  <dt>{t('trust.verification')}</dt><dd>{t('file.unsignedShort')}</dd>
                </dl>
              ) : (
                <dl className="dk-kv">
                  <dt>{t('trust.digest')}</dt><dd className="dk-mono">{shortDigest(entry.digest)}</dd>
                  <dt>{t('trust.verification')}</dt><dd>{entry.policy.requireSignature ? t('trust.signatureRequired') : t('trust.digestOnly')}</dd>
                </dl>
              )}
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
              {local && local.installed ? t('file.upgraded', { name: entry.title, version: local.version }) : t('browse.installedOn', { name: entry.title })}
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
          {step === 'ask' && <Button onClick={onClose}>{t('catalog.cancel')}</Button>}
          {step === 'ask' && <Button variant="primary" onClick={start} disabled={host.status === 'loading' || (Boolean(local) && host.status !== 'ready') || Boolean(block) || !gate.allowed} data-testid="install-confirm">{local && local.installed ? t('file.upgradeAction', { version: local.version }) : t('flow.installAndTurnOn')}</Button>}
          {running && <Button variant="primary" icon="download" busy disabled>{step === 'install' ? t('flow.installing', { name: entry.title }) : t('flow.turningOn', { name: entry.title })}</Button>}
          {step === 'failed' && <Button onClick={onClose}>{t('catalog.close')}</Button>}
          {step === 'failed' && !actions.unknown[entry.id] && <Button variant="primary" icon="refresh" onClick={retry}>{t('catalog.retry')}</Button>}
          {step === 'done' && <Button variant="primary" onClick={onClose}>{t('catalog.close')}</Button>}
        </div>
      </div>
    </div>
  )
}
