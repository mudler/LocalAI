/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { chatChangesApi, filesApi } from '../utils/api'
import { byteText } from '../utils/changeProposal'
import { allFiles, groupId, groupKind, indexFits, indexUse } from '../utils/memoryFiles'
import Icon from './Icon'
import { Button, Callout, EmptyState, LoadError, SkeletonRows } from './ui'
import './memorytab.css'

const READ_LIMIT = 64 * 1024
const path = (p) => (p.startsWith('/') ? p : `/${p}`)

// The size of an index against its caps, as a bar and words. The bar is a
// progress element, so it is read as "x of y" without a style.
function Use({ group, t }) {
  const u = indexUse(group)
  if (!u.index || !u.maxBytes) return null
  const words = t(u.maxLines ? 'memoryPage.use' : 'memoryPage.useBytes', { size: byteText(u.bytes), max: byteText(u.maxBytes), lines: u.lines, maxLines: u.maxLines })
  return (
    <p className={`mt-use${u.over ? ' mt-use--over' : ''}`} data-testid="memory-use">
      <progress className="mt-use__bar" value={Math.min(u.bytes, u.maxBytes)} max={u.maxBytes} aria-label={t('memoryPage.useLabel')} />
      <span>{words}</span>
      {u.over && <b>{' '}{t('memoryPage.over')}</b>}
    </p>
  )
}

// One file of a group: its name and size, and a panel that shows the text and
// switches to an editor. The owner writes straight into their own files here;
// the host keeps every write as a change set, and a delete goes to the Trash.
function FileRow({ file, group, t, busy, onDelete, onSaved, setError }) {
  const uid = useId()
  const [open, setOpen] = useState(false)
  const [text, setText] = useState(null)
  const [edit, setEdit] = useState(null)
  const [saving, setSaving] = useState(false)
  const [ask, setAsk] = useState(false)

  const toggle = async () => {
    if (open) { setOpen(false); setEdit(null); return }
    setOpen(true)
    if (text === null) {
      try { setText(await filesApi.readText(path(file.path), READ_LIMIT)) } catch (e) { setText(''); setError(e.message) }
    }
  }
  const fit = file.index && edit !== null ? indexFits(group, edit) : null
  const save = async () => {
    setSaving(true)
    setError('')
    try {
      await filesApi.uploadWithProgress(path(file.path), new Blob([edit], { type: 'text/markdown' }), { replace: true })
      setText(edit)
      setEdit(null)
      onSaved(file)
    } catch (e) {
      setError(t('memoryPage.saveFailed', { message: e.message }))
    } finally {
      setSaving(false)
    }
  }

  return (
    <li className="mt-file" data-testid="memory-file" data-path={file.path}>
      <div className="mt-file__row">
        <button type="button" className="mt-file__name" aria-expanded={open} aria-controls={`${uid}-panel`} onClick={toggle}>
          <Icon name={open ? 'chevron-down' : 'chevron-right'} />
          <span>{file.name}</span>
          {file.index && <span className="mt-tag">{t('memoryPage.index')}</span>}
        </button>
        <span className="mt-file__size">{byteText(file.size)}</span>
        {ask ? (
          <span className="mt-file__acts" role="group" aria-label={t('memoryPage.confirmDelete', { name: file.name })}>
            <Button size="sm" variant="danger" disabled={busy} data-testid="memory-delete-yes" onClick={() => { setAsk(false); onDelete(file) }}>{t('memoryPage.deleteYes')}</Button>
            <Button size="sm" variant="ghost" onClick={() => setAsk(false)}>{t('proposal.cancelEdit')}</Button>
          </span>
        ) : (
          <span className="mt-file__acts">
            <Button size="sm" variant="ghost" icon="trash" disabled={busy} aria-label={t('memoryPage.delete', { name: file.name })} title={t('memoryPage.delete', { name: file.name })} data-testid="memory-delete" onClick={() => setAsk(true)} />
          </span>
        )}
      </div>
      {open && (
        <div className="mt-panel" id={`${uid}-panel`}>
          {text === null ? <SkeletonRows count={2} /> : edit === null ? (
            <>
              <pre className="mt-text" tabIndex={0} aria-label={file.name} data-testid="memory-text">{text || t('memoryPage.emptyFile')}</pre>
              <Button size="sm" icon="edit" data-testid="memory-edit" onClick={() => setEdit(text)}>{t('memoryPage.edit')}</Button>
            </>
          ) : (
            <form onSubmit={(e) => { e.preventDefault(); if (!fit || fit.fits) save() }} onKeyDown={(e) => { if (e.key === 'Escape') { e.stopPropagation(); setEdit(null) } }}>
              <label className="dk-label" htmlFor={`${uid}-ta`}>{t('memoryPage.editLabel', { name: file.name })}</label>
              <textarea id={`${uid}-ta`} className="dk-textarea mt-edit" rows={Math.min(16, Math.max(6, edit.split('\n').length + 1))} value={edit} spellCheck={false} autoFocus data-testid="memory-edit-text" aria-describedby={`${uid}-n`} onChange={(e) => setEdit(e.target.value)} />
              <p className={`mt-help${fit && !fit.fits ? ' mt-help--err' : ''}`} id={`${uid}-n`} data-testid="memory-edit-size">
                {fit
                  ? t(group.max_lines ? 'memoryPage.editSizeIndex' : 'memoryPage.editSize', { size: byteText(fit.bytes), max: byteText(group.max_bytes), lines: fit.lines, maxLines: group.max_lines })
                  : byteText(new TextEncoder().encode(edit).length)}
                {fit && !fit.fits && <b>{' '}{t('memoryPage.tooBig')}</b>}
              </p>
              <span className="mt-acts">
                <Button type="submit" variant="primary" size="sm" busy={saving} disabled={fit && !fit.fits}>{t('memoryPage.save')}</Button>
                <Button size="sm" onClick={() => setEdit(null)}>{t('proposal.cancelEdit')}</Button>
              </span>
            </form>
          )}
        </div>
      )}
    </li>
  )
}

function groupTitle(g, t) {
  const kind = groupKind(g)
  return t(`memoryPage.group.${kind}`, { key: g.key })
}

// Settings > Memory: the notes the host puts in the prompt of a chat, with
// their sizes against the caps that keep a turn small. It lists the files of
// every source the host knows (the user's, each agent's, each connector
// conversation's) and the agent profiles. The owner can read, edit and delete
// them here; a model changes them only through a confirmed proposal.
export default function MemoryTab({ addToast }) {
  const { t } = useTranslation('chats')
  const [data, setData] = useState(null)
  const [error, setError] = useState(null)
  const [note, setNote] = useState('')
  const [fail, setFail] = useState('')
  const [busy, setBusy] = useState(false)
  const [clear, setClear] = useState(false)

  const load = useCallback(async () => {
    try {
      setData(await chatChangesApi.contextFiles())
      setError(null)
    } catch (e) {
      setError(e)
    }
  }, [])
  useEffect(() => { load() }, [load])

  const run = async (call, say) => {
    setBusy(true)
    setFail('')
    try {
      await call()
      setNote(say)
      addToast?.(say, 'success')
    } catch (e) {
      setFail(e.message)
    } finally {
      setBusy(false)
      await load()
    }
  }
  const remove = (file) => run(() => filesApi.remove(path(file.path)), t('memoryPage.deleted', { name: file.name }))
  const removeAll = async () => {
    const files = allFiles(data?.groups)
    setClear(false)
    await run(async () => { for (const f of files) await filesApi.remove(path(f.path)) }, t('memoryPage.cleared', { count: files.length }))
  }

  if (error) return <LoadError title={t('memoryPage.loadError')} error={error} onRetry={load} testId="memory-load-error" />
  if (!data) return <div data-testid="memory-tab"><SkeletonRows count={3} /></div>

  const groups = data.groups || []
  const total = allFiles(groups).length
  const lim = data.limits || {}
  return (
    <div className="mt-tab" data-testid="memory-tab">
      <p className="mt-lede">{t('memoryPage.lede')}</p>
      <p className="mt-limits" data-testid="memory-limits">
        {t('memoryPage.limits', { lines: lim.index_lines, index: byteText(lim.index_bytes), profile: byteText(lim.profile_bytes), turn: byteText(lim.turn_bytes) })}
      </p>
      <p className="mt-lede"><Link className="cs-link" to="/app/chats/proposals">{t('memoryPage.proposalsLink')}</Link></p>
      <div className="sr-only" role="status" aria-live="polite">{note}</div>
      {fail && <p className="mt-help mt-help--err" role="alert" data-testid="memory-error">{fail}</p>}
      {groups.length === 0 ? (
        <EmptyState variant="first-run" icon="memory" title={t('memoryPage.emptyTitle')} body={t('memoryPage.emptyBody')} testId="memory-empty" />
      ) : (
        <>
          {groups.map((g) => (
            <section key={groupId(g)} className="mt-group" aria-labelledby={`mt-${groupId(g)}`} data-testid="memory-group" data-group={groupId(g)}>
              <h3 className="mt-group__title" id={`mt-${groupId(g)}`}>{groupTitle(g, t)}</h3>
              <Use group={g} t={t} />
              <ul className="mt-files">
                {g.files.map((f) => (
                  <FileRow key={f.path} file={f} group={g} t={t} busy={busy} onDelete={remove} setError={setFail}
                    onSaved={(file) => { setNote(t('memoryPage.saved', { name: file.name })); load() }} />
                ))}
              </ul>
            </section>
          ))}
          <div className="mt-foot">
            {clear ? (
              <Callout tone="warn" role="alertdialog" testId="memory-clear-confirm"
                actions={(
                  <>
                    <Button variant="danger" size="sm" autoFocus busy={busy} data-testid="memory-clear-yes" onClick={removeAll}>{t('memoryPage.clearYes', { count: total })}</Button>
                    <Button variant="ghost" size="sm" onClick={() => setClear(false)}>{t('proposal.cancelEdit')}</Button>
                  </>
                )}
              >{t('memoryPage.clearAsk', { count: total })}</Callout>
            ) : (
              <Button variant="danger" size="sm" icon="trash" disabled={busy} data-testid="memory-clear" onClick={() => setClear(true)}>{t('memoryPage.clear')}</Button>
            )}
          </div>
        </>
      )}
    </div>
  )
}
