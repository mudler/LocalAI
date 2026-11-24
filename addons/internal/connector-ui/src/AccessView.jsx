/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// "Check it": ask what the form will do (Try it), and read what the host refused
// (Recent refusals), the add-on writes that wait for a person and the latest
// calls of add-on tools. The matrix says what the form will do for each kind
// of sender; this section is the proof and the log of what happened.
import { useCallback, useEffect, useState } from 'react'
import { Notice } from './controls'
import { connectorsApi } from './api'
import { pageLocale } from './connector'
import Refusals from './Refusals'
import TryIt from './TryIt'
import ApprovalsView from './ApprovalsView'
import NoteSaveView from './NoteSaveView'
import { toolOutcomeRows } from './access'
import { personText } from './people'
import { usePeople } from './Person'

export default function AccessView({ binding, capabilities, s, version, onAllowed, setForm, preview, kindInfo, ctx, paired, isChannel, mixed, chatNames, senders }) {
  const [view, setView] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const id = binding.id
  const load = useCallback(async () => {
    if (!id) return
    setLoading(true); setError('')
    try { setView(await connectorsApi.access(id)) } catch (e) { setError(e?.message || '') } finally { setLoading(false) }
  }, [id])
  useEffect(() => { load() }, [load, version])
  // One click puts a refused person on the allowed list.
  const allowPerson = async (sender) => {
    setError('')
    try { await connectorsApi.allow(id, sender); await load(); onAllowed?.(sender) } catch (e) { setError(e?.message || '') }
  }
  const titleOf = (name) => (name === 'tools.write' ? s('matrix.row.tools') : (capabilities || []).find((c) => c.name === name)?.title || name)
  return (
    <fieldset className="cn-group" data-testid="connector-access">
      <legend>{s('access.title')}</legend>
      <TryIt form={binding} capabilities={capabilities || []} preview={preview} kindInfo={kindInfo} s={s} ctx={ctx} paired={paired} isChannel={isChannel} mixed={mixed} chatNames={chatNames} />
      {!id ? <p className="cn-muted">{s('access.needSave')}</p> : (<>
        {error && <Notice tone="error" role="alert">{error}</Notice>}
        <div>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-busy={loading ? 'true' : undefined} disabled={loading} onClick={load}>{s('access.refresh')}</button>
        </div>
        {view && (<>
          <Refusals recent={view.recent} senders={senders} titleOf={titleOf} form={binding} setForm={setForm} onAllow={allowPerson} s={s} />
          <NoteSaveView subjects={view.subjects} s={s} testId="connector-access-notesave" />
          <ApprovalsView approvals={view.approvals} s={s} testId="connector-access-approvals" />
          <ToolOutcomes rows={toolOutcomeRows(view)} s={s} />
        </>)}
      </>)}
    </fieldset>
  )
}

// The latest calls of add-on tools. A row holds the tool, how the call ended and
// why, in the owner's words: never what was asked or answered.
function ToolOutcomes({ rows, s }) {
  const people = usePeople()
  return (
    <div>
      <p className="dk-hint">{s('access.toolsHelp')}</p>
      {rows.length === 0 ? <p className="cn-muted">{s('access.toolsNone')}</p> : (
        <ul className="cn-runs" aria-label={s('access.toolsTitle')} data-testid="connector-tool-outcomes">
          {rows.map((r) => (
            <li key={r.key} data-outcome={r.outcome}>
              <span className={`cn-status cn-status--${r.tone}`}>{s(`access.outcome.${r.outcome}`)}</span>
              <span className="cn-mono">{r.tool}</span>
              <span className="cn-muted">{new Date(r.at).toLocaleString(pageLocale(), { dateStyle: 'short', timeStyle: 'medium' })}</span>
              {r.message && <span className="cn-muted">{r.message}</span>}
              {r.senders.length > 0 && <span className="cn-muted cn-mono">{s('access.who', { senders: r.senders.map((x, k) => personText(people, x, k === 0 ? r.senderName : '')).join(', ') })}</span>}
              {r.asker && <span className="cn-muted cn-mono">{s('access.askedBy', { sender: personText(people, r.asker) })}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
