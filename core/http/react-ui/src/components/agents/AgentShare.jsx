// SPDX-License-Identifier: MIT
/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import ShareDialog from '../share/ShareDialog'
import { kindShareClient } from '../share/shareClients'
import { chatAgentsApi, sharesApi } from '../../utils/api'
import { addonsApi } from '../../utils/addonsClient'
import { resourceRequest, resourceOperationId } from '../../addons/resourceCalls'
import { AGENT_KIND, applyReferenceChoices, needsSummary, referenceRows } from '../../utils/agentShare'
import { Button, Callout, LoadingSpinner, StatusChip } from '../ui'
import Dialog from '../Dialog'
import { agentPath } from './AgentBits'
import './agentshare.css'

// A grant on an add-on resource, through the add-on routes: the owner's own
// browser session, confirmed with the resource ID.
async function addonGrant(addonId, resourceId, principal, role) {
  const r = resourceRequest({ op: 'grant-set', addonId, resourceId, userId: principal, role, operationId: resourceOperationId() })
  return addonsApi.call(r.path, { method: r.method, body: r.body, headers: r.headers })
}

// The Share dialog of a saved agent. It asks the server for the agent's ID in
// the sharing API and for what the agent uses, then opens the common Share
// dialog with a row for each of those things.
export function AgentShareDialog({ agent, close, onChanged, addToast }) {
  const { t } = useTranslation(['agents', 'common'])
  const [state, setState] = useState({ phase: 'loading', ref: '', references: [], error: null })

  useEffect(() => {
    let live = true
    Promise.all([chatAgentsApi.shareRef(agent.id), chatAgentsApi.references(agent.id)])
      .then(([ref, refs]) => { if (live) setState({ phase: 'ready', ref: ref.id, references: refs.references || [], error: null }) })
      .catch((error) => { if (live) setState({ phase: error?.status === 409 ? 'off' : 'error', ref: '', references: [], error }) })
    return () => { live = false }
  }, [agent.id])

  const client = useMemo(() => (state.ref ? kindShareClient(sharesApi, AGENT_KIND, state.ref) : null), [state.ref])
  const rows = useMemo(() => referenceRows(state.references, t), [state.references, t])
  const roles = useMemo(() => [
    { id: 'use', label: t('share.roles.use'), help: t('share.roles.useHelp') },
    { id: 'viewer', label: t('share.roles.viewer'), help: t('share.roles.viewerHelp') },
  ], [t])

  const onShare = useCallback(async ({ principal, choices }) => {
    const out = await applyReferenceChoices({ principal, references: state.references, choices, sharesApi, addonGrant })
    if (out.applied > 0) addToast?.(t('share.refsApplied', { count: out.applied }), 'success')
    if (out.failed.length > 0) addToast?.(t('share.refsFailed', { names: out.failed.join(', ') }), 'error', 8000)
    if (out.skipped.length > 0) addToast?.(t('share.refsSkipped', { names: out.skipped.join(', ') }), 'warning', 8000)
  }, [state.references, addToast, t])

  if (state.phase === 'ready' || state.phase === 'off') {
    return (
      <ShareDialog
        client={client}
        resource={{ id: agent.id, name: agent.name }}
        roles={roles}
        allowEveryone
        sharing={state.phase === 'ready'}
        intro={t('share.intro')}
        references={rows}
        onShare={onShare}
        onChanged={onChanged}
        onLost={close}
        close={close}
        testId="agent-share-dialog"
      />
    )
  }
  return (
    <Dialog
      title={t('common:share.title', { name: agent.name })}
      onClose={close}
      closeLabel={t('common:actions.close')}
      testId="agent-share-dialog"
      foot={<Button onClick={close}>{t('common:actions.close')}</Button>}
    >
      {state.phase === 'loading'
        ? <p className="ag-share__status" role="status"><LoadingSpinner size="sm" /> {t('common:share.loading')}</p>
        : <Callout tone="error" role="alert" testId="agent-share-error">{t('share.failed', { message: state.error?.message || '' })}</Callout>}
    </Dialog>
  )
}

// useAgentNeeds reads what an agent needs and its state for the caller.
function useAgentNeeds(agentId) {
  const [s, setS] = useState({ loaded: false, needs: [], error: null })
  useEffect(() => {
    if (!agentId) { setS({ loaded: false, needs: [], error: null }); return undefined }
    let live = true
    chatAgentsApi.needs(agentId)
      .then(r => { if (live) setS({ loaded: true, needs: r.needs || [], error: null }) })
      .catch(error => { if (live) setS({ loaded: true, needs: [], error }) })
    return () => { live = false }
  }, [agentId])
  return s
}

const STATE_TONE = { ok: 'ok', missing: 'warn', unavailable: 'idle', ask: 'warn' }

// The list of what an agent needs, on its page.
export function AgentNeedsList({ agentId }) {
  const { t } = useTranslation('agents')
  const { loaded, needs, error } = useAgentNeeds(agentId)
  const sum = needsSummary(needs)
  return (
    <section className="ag-group" aria-labelledby="ag-needs-h" data-testid="agent-needs">
      <div className="ag-section-head">
        <h2 className="ag-h2" id="ag-needs-h">{t('needs.title')}</h2>
      </div>
      <p className="ag-note">{t('needs.intro')}</p>
      {!loaded ? <p className="ag-note" role="status"><LoadingSpinner size="sm" /></p>
        : error ? <Callout tone="error" role="alert">{t('needs.loadError')}</Callout>
          : sum.total === 0 ? <p className="ag-note" data-testid="agent-needs-none">{t('needs.none')}</p>
            : (
              <ul className="ag-runs ag-needs">
                {sum.all.map((n, i) => (
                  <li key={`${n.kind}-${n.label}-${i}`} className="ag-needs__row" data-state={n.state} data-testid="agent-need">
                    <span className="ag-needs__main">
                      <span className="ag-needs__label">{n.label}</span>
                      <span className="ag-needs__kind">{t(`share.kind.${n.kind}`, { defaultValue: n.kind })}</span>
                    </span>
                    <StatusChip tone={STATE_TONE[n.state]} label={t(`needs.state.${n.state}`)} testId="agent-need-state" />
                  </li>
                ))}
              </ul>
            )}
    </section>
  )
}

// needLabel names one need with its kind, for a sentence: "Network: all".
function needLabel(n, t) {
  return `${t(`share.kind.${n.kind}`, { defaultValue: n.kind })}: ${n.label}`
}

// The notice a chat with an agent shows before the first message when the
// person lacks something the agent needs, or when the agent asks for a
// network or built-in tools that the chat does not start with.
export function AgentNeedsNotice({ agentId }) {
  const { t } = useTranslation('agents')
  const { loaded, needs, error } = useAgentNeeds(agentId)
  const sum = needsSummary(needs)
  if (!loaded || error || (sum.lacking.length === 0 && sum.asks.length === 0)) return null
  const link = <Link className="cx-link" to={agentPath(agentId)}>{t('needs.noticeLink')}</Link>
  return (
    <div className="stack">
      {sum.lacking.length > 0 && (
        <Callout tone="warn" testId="agent-needs-notice" title={t('needs.noticeTitle', { count: sum.lacking.length })} actions={link}>
          {sum.lacking.map(n => n.label).join(', ')}
        </Callout>
      )}
      {sum.asks.length > 0 && (
        <Callout tone="info" testId="agent-needs-asks" title={t('needs.asksTitle', { count: sum.asks.length })} actions={sum.lacking.length > 0 ? null : link}>
          <p>{sum.asks.map(n => needLabel(n, t)).join(', ')}</p>
          <p className="text-note">{t('needs.asksHelp')}</p>
        </Callout>
      )}
    </div>
  )
}
