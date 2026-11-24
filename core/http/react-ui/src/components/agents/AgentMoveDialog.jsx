/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Icon from '../Icon'
import { Button, Callout, Segmented, Switch } from '../ui'
import AgentFolderDialog from './AgentFolderDialog'
import AgentProjectFiles from './AgentProjectFiles'
import { chatAgentsApi } from '../../utils/api'
import { effectsOf, hardBlockers, moveBody, moveReady, needsDisable, needsRevoke, suggestedFolder } from '../../utils/agentProject'
import './agentparts.css'

// The dialog that moves a saved agent to another project, or takes it out of
// its project. It asks the server for a dry run each time the choice changes,
// lists what the move touches, and only the confirm button moves the agent.
//   agent       the saved agent ({ id, name })
//   target      the project to move to ({ id, name, slug, ... }), or null to
//               leave the project
//   from        the project the agent is in now (null when it is gone)
//   detach      true when the dialog was opened by the "Detach" action of a
//               deleted project: the texts say so
//   onDone      called with the report when the agent was moved
//   onCancel    closes the dialog
export default function AgentMoveDialog({ agent, target, from, detach = false, onDone, onCancel }) {
  const { t } = useTranslation('agents')
  const targetId = target?.id || ''
  const [filesMode, setFilesMode] = useState('write')
  const [wholeHome, setWholeHome] = useState(false)
  const [folders, setFolders] = useState(() => {
    const s = targetId ? null : suggestedFolder(from)
    return s ? [s] : []
  })
  const [choosing, setChoosing] = useState(false)
  const [revokeShares, setRevokeShares] = useState(false)
  const [disableTriggers, setDisableTriggers] = useState(false)
  const [report, setReport] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const opts = { filesMode, folders, wholeHome, revokeShares, disableTriggers }
  const ready = moveReady(targetId, opts)
  const key = JSON.stringify(moveBody(targetId, opts, true))

  // The dry run follows the choice. An answer that arrives after the choice
  // changed again is dropped.
  useEffect(() => {
    if (!ready) { setReport(null); return undefined }
    let live = true
    setLoading(true)
    setError('')
    chatAgentsApi.moveProject(agent.id, JSON.parse(key))
      .then((r) => { if (live) { setReport(r); setLoading(false) } })
      .catch((err) => { if (live) { setReport(null); setError(err.message || String(err)); setLoading(false) } })
    return () => { live = false }
  }, [agent.id, key, ready])

  const hard = hardBlockers(report)
  const effects = effectsOf(report)
  const blocked = hard.length > 0 || (needsRevoke(report) && !revokeShares) || (needsDisable(report) && !disableTriggers)
  const canMove = ready && Boolean(report) && !loading && !blocked && !busy

  const move = async () => {
    setBusy(true)
    setError('')
    try {
      const done = await chatAgentsApi.moveProject(agent.id, moveBody(targetId, opts, false))
      onDone(done)
    } catch (err) {
      setError(err.message || String(err))
      setBusy(false)
    }
  }

  const sentence = (e) => t(`move.effect.${e.key}`, { count: e.count, names: (e.names || []).join(', ') })
  const title = detach ? t('move.detachTitle') : targetId ? t('move.title', { name: target.name }) : t('move.leaveTitle')
  const text = detach
    ? t('move.detachText')
    : targetId ? t('move.text', { agent: agent.name, name: target.name }) : t('move.leaveText', { agent: agent.name, name: from?.name || t('project.deleted') })

  return (
    <>
      <Dialog
        title={title}
        description={text}
        onClose={onCancel}
        pending={busy}
        closeLabel={t('move.cancel')}
        labelId="ag-move-title"
        testId="agent-move-dialog"
        wide
        bodyClass="ag-move-scroll"
        foot={(
          <>
            <Button variant="ghost" onClick={onCancel} disabled={busy}>{t('move.cancel')}</Button>
            <Button variant="primary" icon="check" busy={busy} disabled={!canMove} onClick={move} data-testid="agent-move-confirm">
              {detach ? t('move.detach') : t('move.confirm')}
            </Button>
          </>
        )}
      >
        <div className="ag-move" data-testid="agent-move-body">
          {targetId ? (
            <AgentProjectFiles project={target} mode={filesMode} onMode={setFilesMode} />
          ) : (
            <div className="ag-move__keep" data-testid="agent-move-keep">
              <Segmented
                label={t('move.keepLabel')} testId="agent-move-scope" itemTestId="agent-move-scope-"
                value={wholeHome ? 'all' : 'one'} onChange={(v) => setWholeHome(v === 'all')}
                options={[{ id: 'one', label: t('move.keepOne') }, { id: 'all', label: t('move.keepAll') }]}
              />
              {wholeHome ? (
                <p className="ag-note" data-testid="agent-move-all-help">{t('move.keepAllHelp')}</p>
              ) : (
                <div className="ag-move__folder">
                  {folders[0]?.path ? (
                    <span className="ag-folder__path" data-testid="agent-move-folder"><Icon name="folder" />{folders[0].path}</span>
                  ) : (
                    <span className="ag-note" data-testid="agent-move-nofolder">{t('move.keepNone')}</span>
                  )}
                  <Button size="sm" icon="folder" onClick={() => setChoosing(true)} data-testid="agent-move-choose">{folders[0]?.path ? t('folder.change') : t('folder.choose')}</Button>
                  <Segmented
                    label={t('folder.modeLabel')} testId="agent-move-mode" itemTestId="agent-move-mode-"
                    value={folders[0]?.mode === 'write' ? 'write' : 'read'}
                    onChange={(mode) => setFolders((prev) => (prev[0] ? [{ ...prev[0], mode }] : prev))}
                    options={[{ id: 'read', label: t('folder.read') }, { id: 'write', label: t('folder.write') }]}
                  />
                </div>
              )}
            </div>
          )}

          <section aria-labelledby="ag-move-effects-h" className="ag-move__effects" aria-live="polite" data-testid="agent-move-effects">
            <h4 className="ag-h3" id="ag-move-effects-h">{t('move.effectsTitle')}</h4>
            {!ready && <p className="ag-note">{t('move.pickFolder')}</p>}
            {ready && loading && <p className="ag-note" role="status">{t('move.checking')}</p>}
            {ready && !loading && report && (
              <ul className="ag-move__list">
                {effects.map((e) => <li key={e.key} data-effect={e.key}>{sentence(e)}</li>)}
                <li data-effect="notes">{t('move.effect.notes')}</li>
                <li data-effect="folders">{targetId ? t('move.effect.foldersInto', { name: target.name }) : t('move.effect.foldersOut')}</li>
              </ul>
            )}
          </section>

          {hard.length > 0 && <Callout tone="error" role="alert" testId="agent-move-busy">{t('move.blocked.busy')}</Callout>}
          {needsRevoke(report) && (
            <div className="ag-move__switch" data-testid="agent-move-revoke">
              <Switch checked={revokeShares} onChange={setRevokeShares} labelledBy="ag-move-revoke-l" testId="agent-move-revoke-switch" />
              <span id="ag-move-revoke-l">{t('move.revoke', { count: report.shares })}</span>
            </div>
          )}
          {needsDisable(report) && (
            <div className="ag-move__switch" data-testid="agent-move-disable">
              <Switch checked={disableTriggers} onChange={setDisableTriggers} labelledBy="ag-move-disable-l" testId="agent-move-disable-switch" />
              <span id="ag-move-disable-l">{t('move.disable', { count: report.triggers_outside.length, names: report.triggers_outside.map((x) => x.name).join(', ') })}</span>
            </div>
          )}
          {error && <p className="dk-field-error" role="alert" data-testid="agent-move-error">{error}</p>}
        </div>
      </Dialog>
      {choosing && (
        <AgentFolderDialog
          start={folders[0]?.path || '/'}
          onCancel={() => setChoosing(false)}
          onChoose={(path) => { setChoosing(false); setFolders([{ path, mode: folders[0]?.mode || 'read' }]) }}
        />
      )}
    </>
  )
}
