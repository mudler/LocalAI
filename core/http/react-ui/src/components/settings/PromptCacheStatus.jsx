/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { promptCacheApi } from '../../utils/api'
import { formatBytes } from '../../utils/format'
import { promptCacheMessage, promptCacheOffLabel, promptCacheOffMessage, promptCacheState, promptCacheTotal } from '../../utils/promptCache'
import { Button, Callout, StatusChip, useLoad } from '../ui'
import ConfirmDialog from '../ConfirmDialog'
import Icon from '../Icon'

// The disk prompt cache of each node: size, free disk, effective cap and
// state, with the Clear button. Admin only (GET /api/prompt-cache). In
// distributed mode each worker is a node, with a total line; a worker that did
// not answer shows why.
export default function PromptCacheStatus({ addToast }) {
  const load = useLoad(() => promptCacheApi.status(), [])
  const [confirm, setConfirm] = useState(false)
  const [clearing, setClearing] = useState(false)

  if (load.status !== 'ready') return null
  const data = load.data || {}
  const nodes = data.nodes || []
  const total = promptCacheTotal(data)
  const several = nodes.length > 1

  const clear = async () => {
    setClearing(true)
    try {
      const res = await promptCacheApi.clear()
      addToast?.(`Cleared ${res.files || 0} cached ${res.files === 1 ? 'prompt' : 'prompts'} (${formatBytes(res.bytes || 0)}).`, 'success')
      load.retry()
    } catch (err) {
      addToast?.(err.message, 'error')
    } finally {
      setClearing(false)
      setConfirm(false)
    }
  }

  return (
    <div className="st-now" data-testid="settings-prompt-cache">
      <Icon name="hard-drive" aria-hidden="true" />
      <div className="st-now__text">
        {!data.enabled && (
          <p><strong>The disk prompt cache is off.</strong> It needs a data path and the setting below.</p>
        )}
        {nodes.length === 0 && data.enabled && (
          <p><strong>Nothing cached on disk yet.</strong></p>
        )}
        {total && <p data-testid="prompt-cache-total"><strong>{total}</strong></p>}
        {nodes.map(n => {
          const st = n.status || {}
          const s = promptCacheState(st)
          const off = n.enabled === false
          const msg = off ? promptCacheOffMessage(n.off_reason) : promptCacheMessage(st)
          if (n.error && !n.files) {
            return (
              <div key={n.node} data-testid={`prompt-cache-node-${n.node}`}>
                <p>
                  <StatusChip tone="idle" label="No answer" testId="prompt-cache-state" />{' '}
                  <strong>{n.node}</strong>: {n.error === 'offline' ? 'offline. It applies the clears when it is back.' : n.error}
                </p>
              </div>
            )
          }
          return (
            <div key={n.node} data-testid={`prompt-cache-node-${n.node}`}>
              <p>
                {off
                  ? <StatusChip tone="idle" label={promptCacheOffLabel(n.off_reason)} testId="prompt-cache-state" />
                  : <StatusChip tone={s.tone} label={s.label} testId="prompt-cache-state" />}{' '}
                {several && <><strong>{n.node}</strong>: </>}
                <strong>{formatBytes(n.bytes || 0)} in {n.files || 0} {n.files === 1 ? 'entry' : 'entries'}.</strong>{' '}
                {st.total_bytes > 0 && <>Free disk {formatBytes(st.free_bytes)} of {formatBytes(st.total_bytes)}. </>}
                {st.effective_cap_bytes > 0 && <>The cache may grow to {formatBytes(st.effective_cap_bytes)}.</>}
                {st.shares_models_disk && <> It shares the disk with the models, so it never takes the room a download needs.</>}
              </p>
              {msg && <Callout tone={!off && st.state === 'disabled_disk_full' ? 'error' : 'warn'} testId="prompt-cache-message">{msg}</Callout>}
            </div>
          )
        })}
      </div>
      <Button size="sm" variant="ghost" disabled={clearing || nodes.every(n => !n.files)} onClick={() => setConfirm(true)} data-testid="prompt-cache-clear">
        Clear
      </Button>
      <ConfirmDialog
        open={confirm}
        danger
        title="Clear the disk prompt cache?"
        message={several
          ? 'This deletes the cached prompts of every user and every model on every node. A node that is offline deletes them when it is back. Later requests compute their prompts again.'
          : 'This deletes the cached prompts of every user and every model on this node. Later requests compute their prompts again.'}
        confirmLabel="Clear"
        pending={clearing}
        onConfirm={clear}
        onCancel={() => setConfirm(false)}
      />
    </div>
  )
}
