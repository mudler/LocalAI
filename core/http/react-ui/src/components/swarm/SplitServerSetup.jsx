/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { nodesApi, p2pSplitApi } from '../../utils/api'
import { shortPeer } from '../../utils/p2p'
import { Button, Callout } from '../ui'
import SwarmDialog from './SwarmDialog'
import { backendNodes } from './splitWorkersModel'

// The quick setup of a split server: the admin picks a backend node, sees
// what changes on it, picks the model-split workers to attach, and confirms.
// Nothing changes before the confirm click: a node becomes a split server
// only by this deliberate action or by its own start flag.
//
//   nodes      the list of GET /api/nodes
//   workers    the model-split workers (GET /api/p2p/split-workers)
//   initial    the node to preselect, or ''
export default function SplitServerSetup({ nodes, workers, initial = '', onClose, onDone, addToast }) {
  const { t } = useTranslation('swarm')
  const choices = backendNodes(nodes)
  const [node, setNode] = useState(initial || choices.find(n => n.splitServer)?.id || choices[0]?.id || '')
  const chosen = choices.find(n => n.id === node)
  const helpers = (workers || []).filter(w => w.node_id !== node)
  const [picked, setPicked] = useState(() => new Set(helpers.filter(w => !w.node_id).map(w => w.id)))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const toggle = id => setPicked(current => {
    const next = new Set(current)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })
  const confirm = async () => {
    if (!chosen) return
    setBusy(true)
    setError('')
    try {
      if (!chosen.splitServer) await nodesApi.setSplitServer(chosen.id, true)
      const attach = helpers.filter(w => picked.has(w.id))
      for (const w of attach) await p2pSplitApi.setNode(w.id, chosen.id)
      addToast?.(t('splitServer.toast.set', { node: chosen.name, count: attach.length }), 'success')
      onDone?.()
      onClose()
    } catch (err) {
      setError(err?.message || '')
    } finally {
      setBusy(false)
    }
  }

  const foot = (
    <>
      <Button variant="ghost" onClick={onClose} disabled={busy}>{t('splitServer.cancel')}</Button>
      <Button variant="primary" className="sw-dialog__primary" busy={busy} disabled={!chosen} onClick={confirm} data-testid="split-server-confirm">
        {chosen?.splitServer ? t('splitServer.confirmAttach') : t('splitServer.confirm')}
      </Button>
    </>
  )
  return (
    <SwarmDialog title={t('splitServer.title')} desc={t('splitServer.desc')} foot={foot} onClose={onClose} pending={busy} testId="split-server-setup" wide>
      {choices.length === 0
        ? <Callout tone="warn">{t('splitServer.noNodes')}</Callout>
        : (
          <div className="sw-split-setup">
            <label className="sw-split-node-field">
              <span className="dk-label">{t('splitServer.node')}</span>
              <select className="dk-select" value={node} onChange={event => setNode(event.target.value)} data-testid="split-server-node">
                {choices.map(n => (
                  <option key={n.id} value={n.id}>
                    {n.splitServer ? t('splitServer.nodeDeclared', { name: n.name }) : n.name}
                  </option>
                ))}
              </select>
            </label>
            {chosen && !chosen.splitServer && (
              <div className="sw-split-setup__changes" data-testid="split-server-changes">
                <p className="sw-note">{t('splitServer.changesTitle', { node: chosen.name })}</p>
                <ul className="sw-split-setup__list">
                  <li>{t('splitServer.changes.ports')}</li>
                  <li>{t('splitServer.changes.relay')}</li>
                  <li>{t('splitServer.changes.load')}</li>
                  <li>{t('splitServer.changes.undo')}</li>
                </ul>
              </div>
            )}
            {chosen && !chosen.online && <Callout tone="warn">{t('splitServer.offline', { node: chosen.name })}</Callout>}
            <fieldset className="sw-split-setup__helpers">
              <legend className="dk-label">{t('splitServer.helpers')}</legend>
              {helpers.length === 0 && <p className="sw-note sw-note--quiet">{t('splitServer.noHelpers')}</p>}
              {helpers.map(w => (
                <label key={w.id} className="sw-split-setup__helper">
                  <input type="checkbox" className="dk-check" checked={picked.has(w.id)} onChange={() => toggle(w.id)} data-testid={`split-server-helper-${w.id}`} />
                  <span className="dk-mono">{w.label || w.name || shortPeer(w.peer_id)}</span>
                  {w.node_id && <span className="sw-hint">{t('splitServer.moveFrom', { node: w.node_name || w.node_id })}</span>}
                </label>
              ))}
            </fieldset>
            {error && <Callout tone="error" role="alert">{error}</Callout>}
          </div>
        )}
    </SwarmDialog>
  )
}
