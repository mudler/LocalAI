/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import SideSheet from '../SideSheet'
import { Button, StatusChip } from '../ui'
import { MAX_NAME } from '../../utils/setups'
import { setupSummary } from '../../utils/setupSummary'
import './setups.css'

// One row of the Manage sheet. The name is a field that saves when it loses
// focus or on Enter; an empty name goes back to the saved one.
function Row({ setup, index, count, isDefault, onRename, onRemove, onMove, onSetDefault }) {
  const { t } = useTranslation('chat')
  const [name, setName] = useState(setup.name)
  const [confirming, setConfirming] = useState(false)
  useEffect(() => { setName(setup.name) }, [setup.name])
  // A refused rename leaves the typed name in the field (the toast says why),
  // so it can be tried again.
  const commit = () => {
    const next = name.trim()
    if (!next || next === setup.name) { setName(setup.name); return }
    onRename(setup.id, next)
  }
  return (
    <div className="setup-manage__row" data-testid={`setup-manage-row-${setup.id}`}>
      <div className="setup-manage__main">
        <input
          className="dk-input"
          value={name}
          maxLength={MAX_NAME}
          aria-label={t('setup.renameLabel')}
          data-testid={`setup-manage-name-${setup.id}`}
          onChange={(e) => setName(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); e.currentTarget.blur() } }}
        />
        <span className="setup-manage__meta">{[setup.model || t('setup.noModel'), setupSummary(setup, t)].join(' · ')}</span>
      </div>
      {confirming ? (
        <span className="setup-manage__confirm">
          {t('setup.removeConfirm', { name: setup.name })}
          <Button size="sm" variant="danger" onClick={() => { setConfirming(false); onRemove(setup.id) }} data-testid={`setup-manage-remove-yes-${setup.id}`}>{t('setup.removeYes')}</Button>
          <Button size="sm" variant="ghost" onClick={() => setConfirming(false)}>{t('setup.cancel')}</Button>
        </span>
      ) : (
        <span className="setup-manage__tools">
          {isDefault
            ? <StatusChip tone="ok" icon="check" testId={`setup-manage-default-${setup.id}`}>{t('setup.default')}</StatusChip>
            : <Button size="sm" variant="ghost" onClick={() => onSetDefault(setup.id)} data-testid={`setup-manage-make-default-${setup.id}`}>{t('setup.makeDefault')}</Button>}
          <Button size="sm" variant="ghost" icon="arrow-up" aria-label={t('setup.moveUp', { name: setup.name })} disabled={index === 0} onClick={() => onMove(setup.id, -1)} data-testid={`setup-manage-up-${setup.id}`} />
          <Button size="sm" variant="ghost" icon="arrow-down" aria-label={t('setup.moveDown', { name: setup.name })} disabled={index === count - 1} onClick={() => onMove(setup.id, 1)} data-testid={`setup-manage-down-${setup.id}`} />
          <Button size="sm" variant="danger-ghost" icon="trash" aria-label={t('setup.remove')} onClick={() => setConfirming(true)} data-testid={`setup-manage-remove-${setup.id}`} />
        </span>
      )}
    </div>
  )
}

// The Manage sheet: rename, reorder, delete and choose the default setup.
// Every change saves at once. A button that goes away or turns disabled by
// its own action hands the focus on (see `after`).
export default function ManageSetups({ list, defaultId, onClose, onRename, onRemove, onMove, onSetDefault }) {
  const { t } = useTranslation('chat')
  const box = useRef(null)
  const done = useRef(null)
  // Where the focus goes once the list shows the change: { id, to } with to
  // 'name', 'up' or 'down'.
  const after = useRef(null)
  const find = (id, kind) => box.current?.querySelector(`[data-testid="setup-manage-${kind}-${id}"]`)
  useEffect(() => {
    const a = after.current
    if (!a) return
    after.current = null
    const idx = list.findIndex((s) => s.id === a.id)
    if (idx < 0) { done.current?.focus(); return }
    let el = a.to === 'name' ? null : find(a.id, a.to)
    if (!el || el.disabled) el = find(a.id, 'name')
    el?.focus()
  }, [list])

  const run = (focus, fn) => async (...args) => {
    after.current = focus(...args)
    const res = await fn(...args)
    if (res === null) after.current = null
    return res
  }
  const remove = run((id) => {
    const i = list.findIndex((s) => s.id === id)
    const next = list[i + 1] || list[i - 1]
    return next ? { id: next.id, to: 'name' } : { id: '', to: 'name' }
  }, onRemove)
  const move = run((id, d) => ({ id, to: d < 0 ? 'up' : 'down' }), onMove)
  const makeDefault = run((id) => ({ id, to: 'name' }), onSetDefault)

  return (
    <SideSheet
      title={t('setup.manageTitle')}
      description={t('setup.manageNote')}
      closeLabel={t('setup.cancel')}
      testId="setup-manage-sheet"
      labelId="setup-manage-title"
      onClose={onClose}
      footer={<Button ref={done} variant="primary" onClick={onClose} data-testid="setup-manage-done">{t('setup.done')}</Button>}
    >
      <div className="setup-manage" ref={box}>
        {list.length === 0 && <p className="setup-manage__note">{t('setup.manageEmpty')}</p>}
        {list.map((s, i) => (
          <Row
            key={s.id} setup={s} index={i} count={list.length} isDefault={s.id === defaultId}
            onRename={onRename} onRemove={remove} onMove={move} onSetDefault={makeDefault}
          />
        ))}
      </div>
    </SideSheet>
  )
}
