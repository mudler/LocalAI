/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import SideSheet from '../SideSheet'
import { Button, Callout, LoadError } from '../ui'
import { chatProjectsApi, filesApi } from '../../utils/api'
import { renderSharedMarkdown } from '../../utils/sharedMarkdown'
import { MEMORY_INDEX, memoryFileError, whenAgo } from '../../utils/projects'

// The most of a topic file the sheet reads. A longer file is not edited here:
// saving a cut copy would drop the rest.
const EDIT_LIMIT = 1024 * 1024

const memoryPath = (project, file) => `/Projects/${project.slug}/memory/${file}`

// The sheet that edits the index and the topic files. Choosing a file reads
// it; Save replaces that one file.
function MemorySheet({ project, memory, onClose, onSaved }) {
  const { t } = useTranslation(['projects', 'common'])
  const [file, setFile] = useState(MEMORY_INDEX)
  const [extra, setExtra] = useState([])
  const [text, setText] = useState(memory.index || '')
  const [state, setState] = useState('ready') // loading | ready | error | large
  const [busy, setBusy] = useState(false)
  const [newName, setNewName] = useState('')
  const [nameError, setNameError] = useState('')
  const [saveError, setSaveError] = useState('')
  const names = useMemo(() => [MEMORY_INDEX, ...memory.files.map(f => f.name).filter(n => n !== MEMORY_INDEX), ...extra], [memory, extra])

  // Each choice takes a token. A read that comes back after a newer choice, or
  // after the sheet closed, is dropped: it must not put the old file's text in
  // the box or set state on a sheet that is gone.
  const readToken = useRef(0)
  useEffect(() => () => { readToken.current = -1 }, [])

  const choose = async (name) => {
    const token = ++readToken.current
    setFile(name)
    setSaveError('')
    if (name === MEMORY_INDEX) { setText(memory.index || ''); setState('ready'); return }
    if (extra.includes(name)) { setText(''); setState('ready'); return }
    const listed = memory.files.find(f => f.name === name)
    // A file that is not read leaves no text of the one before it in the box.
    if (listed && listed.size > EDIT_LIMIT) { setText(''); setState('large'); return }
    setText('')
    setState('loading')
    try {
      const body = await filesApi.readText(memoryPath(project, name), EDIT_LIMIT)
      if (readToken.current !== token) return
      setText(body)
      setState('ready')
    } catch {
      if (readToken.current !== token) return
      setText('')
      setState('error')
    }
  }

  const addName = () => {
    const name = newName.trim()
    const bad = memoryFileError(name)
    if (bad) { setNameError(t(`memory.errors.${bad}`)); return }
    if (names.includes(name)) { setNameError(t('memory.errors.exists')); return }
    setNameError('')
    setNewName('')
    readToken.current += 1
    setExtra(list => [...list, name])
    setFile(name)
    setText('')
    setState('ready')
  }

  const save = async () => {
    if (busy || state !== 'ready') return
    setBusy(true)
    setSaveError('')
    try {
      await chatProjectsApi.putMemory(project.id, file, text)
      onSaved(file)
    } catch (err) {
      setSaveError(t('toasts.memoryFailed', { file, message: err.message || String(err) }))
      setBusy(false)
    }
  }

  return (
    <SideSheet
      wide
      title={t('memory.sheetTitle')}
      description={t('memory.sheetBody')}
      testId="project-memory-sheet"
      closeLabel={t('common:actions.close')}
      onClose={onClose}
      footer={(
        <>
          <Button onClick={onClose}>{t('sheet.cancel')}</Button>
          <Button variant="primary" busy={busy} disabled={state !== 'ready'} onClick={save}>{t('sheet.save')}</Button>
        </>
      )}
    >
      <div className="pj-form">
        <div className="dk-field">
          <label className="dk-label" htmlFor="project-memory-file">{t('memory.fileLabel')}</label>
          <div className="dk-select-wrap">
            <select id="project-memory-file" className="dk-select" value={file} onChange={e => choose(e.target.value)}>
              {names.map(n => <option key={n} value={n}>{n}</option>)}
            </select>
          </div>
        </div>
        <div className="dk-field">
          <label className="dk-label" htmlFor="project-memory-text">{t('memory.contentLabel')}</label>
          {state === 'error' && <LoadError title={t('load.memory')} onRetry={() => choose(file)} />}
          {state === 'large' && <Callout tone="warn">{t('memory.tooLarge')}</Callout>}
          <textarea
            id="project-memory-text"
            className="dk-textarea pj-memory-text"
            rows={14}
            value={text}
            disabled={state !== 'ready'}
            onChange={e => setText(e.target.value)}
          />
        </div>
        <div className="dk-field">
          <label className="dk-label" htmlFor="project-memory-new">{t('memory.newFile')}</label>
          <div className="pj-chipadd">
            <input
              id="project-memory-new"
              className="dk-input"
              value={newName}
              placeholder={t('memory.newName')}
              aria-invalid={nameError ? 'true' : undefined}
              onChange={e => setNewName(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); addName() } }}
            />
            <Button size="sm" onClick={addName}>{t('memory.add')}</Button>
          </div>
          {nameError ? <p className="dk-field-error" role="alert">{nameError}</p> : <p className="dk-hint">{t('memory.newHint')}</p>}
        </div>
        {saveError && <p className="dk-field-error" role="alert" data-testid="project-memory-error">{saveError}</p>}
      </div>
    </SideSheet>
  )
}

// The Memory tab: the index drawn as Markdown, the topic files, and Edit.
export default function ProjectMemory({ project, memory, onChanged, addToast, disabled }) {
  const { t, i18n } = useTranslation(['projects', 'common'])
  const [editing, setEditing] = useState(false)
  const html = useMemo(() => renderSharedMarkdown(memory.index || ''), [memory.index])
  return (
    <div className="pj-stack" data-testid="project-memory">
      <section className="pj-section">
        <div className="pj-head">
          <h2 className="pj-h2">{t('memory.title')}</h2>
          <Button size="sm" icon="edit" disabled={disabled} onClick={() => setEditing(true)} data-testid="project-memory-edit">{t('memory.edit')}</Button>
        </div>
        <p className="pj-note">{t('memory.body')}</p>
        <div className="pj-card">
          <h3 className="pj-mono">{t('memory.index')}</h3>
          {memory.index
            ? <div className="pj-md cx-prose" data-testid="project-memory-index" dangerouslySetInnerHTML={{ __html: html }} />
            : <p className="pj-muted">{t('memory.empty')}</p>}
        </div>
        <p className="pj-note" data-testid="project-memory-proposals">{t('memory.proposals')}</p>
      </section>
      <section className="pj-section">
        <h2 className="pj-h2">{t('memory.topics')}</h2>
        {memory.files.filter(f => f.name !== MEMORY_INDEX).length === 0
          ? <p className="pj-muted">{t('memory.noTopics')}</p>
          : (
            <ul className="pj-list">
              {memory.files.filter(f => f.name !== MEMORY_INDEX).map(f => (
                <li key={f.name} className="pj-line" data-testid="project-memory-file">
                  <span className="pj-mono">{f.name}</span>
                  <span className="pj-muted">{whenAgo(f.modified, i18n.language)}</span>
                </li>
              ))}
            </ul>
          )}
      </section>
      {editing && (
        <MemorySheet
          project={project}
          memory={memory}
          onClose={() => setEditing(false)}
          onSaved={(file) => {
            setEditing(false)
            addToast(t('toasts.memorySaved', { file }), 'success')
            onChanged()
          }}
        />
      )}
    </div>
  )
}
