/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import { Button, Callout, Segmented, StatusChip } from '../ui'
import { FileFooter, ProblemDetails } from './WellKnownCard'
import { useWellKnown } from '../../hooks/useWellKnown'
import { chatToolsApi } from '../../utils/api'
import {
  COMMAND_LIMITS, cleanCommands, commandProblems, expandCommand, withArgsFromTemplate,
} from '../../utils/userCommands'
import { parseSource, sourceText, wellKnownProblem, WELL_KNOWN_FILES } from '../../utils/wellKnown'
import './wellKnown.css'
import './commandsEditor.css'

const NEW = '\u0000new'

const blank = () => ({ name: '', description: '', type: 'prompt', template: '', args: [], tool: '' })

// A command of the file as the form edits it: every key a string, the arguments
// in the order of the template.
function toDraft(c) {
  if (c.type === 'tool') return { name: c.name, description: c.description, type: 'tool', template: '', args: [], tool: c.tool || '' }
  return withArgsFromTemplate({ name: c.name, description: c.description, type: 'prompt', template: c.template || '', args: (c.args || []).map(a => ({ name: a.name, default: a.default })), tool: '' })
}

function fileList(doc) {
  const list = Array.isArray(doc?.data?.commands) ? doc.data.commands : []
  return list.filter(c => c && typeof c === 'object' && typeof c.name === 'string')
}

// The tools of a chat, by name, for the suggestions of a tool command. A
// failure leaves the list empty; the field takes any name.
function useToolNames() {
  const [names, setNames] = useState([])
  useEffect(() => {
    let live = true
    chatToolsApi.draft('', '')
      .then((res) => { if (live) setNames([...new Set((res.builtins || []).flatMap(b => b.tools || []))].sort()) })
      .catch(() => {})
    return () => { live = false }
  }, [])
  return names
}

// The slash menu as the composer draws it, with the commands of the form: the
// saved ones, and the one being edited in its place. It is a picture: the
// real menu is in the message box.
function MenuPreview({ rows, builtin, selected }) {
  const { t } = useTranslation('workspace')
  const { t: tc } = useTranslation('chat')
  return (
    <aside className="cm-prev" aria-label={t('commands.preview.title')} data-testid="cm-preview">
      <p className="cm-prev__title"><Icon name="eye" /> {t('commands.preview.title')}</p>
      <div className="dk-cmdlist dk-cmdlist--flat cm-prev__menu" role="presentation">
        <div className="dk-cmd-group">
          <div className="dk-cmd-label">{tc('slash.group.yours')}</div>
          {rows.length === 0 && <p className="cm-prev__none" data-testid="cm-preview-none">{t('commands.preview.none')}</p>}
          {rows.map(r => (
            <div key={r.key} className="dk-cmd cm-prev__row" aria-selected={r.name === selected} data-testid={`cm-preview-row-${r.name || 'new'}`}>
              <Icon name={r.type === 'tool' ? 'wrench' : 'sparkles'} className="dk-icon" />
              <span className="dk-cmd-main">
                <span className="dk-cmd-name dk-mono">/{r.name || '…'}</span>
                <span className="dk-cmd-desc">{r.description}</span>
              </span>
              <span className="dk-badge cm-badge">{tc(`slash.badge.${r.type}`)}</span>
            </div>
          ))}
        </div>
        <div className="dk-cmd-group">
          <div className="dk-cmd-label">{t('commands.preview.builtin')}</div>
          {builtin.map(b => (
            <div key={b.cmd} className="dk-cmd cm-prev__row">
              <Icon name={b.icon} className="dk-icon" />
              <span className="dk-cmd-main">
                <span className="dk-cmd-name dk-mono">{b.cmd}</span>
                <span className="dk-cmd-desc">{b.desc}</span>
              </span>
            </div>
          ))}
        </div>
      </div>
      <div className="cm-prev__box">
        <span className="dk-mono">/</span><span className="cm-prev__caret" aria-hidden="true" />
        <span className="cm-prev__keys">{t('commands.preview.keys')}</span>
      </div>
    </aside>
  )
}

// The form of one command: name, kind, template with its arguments, or tool.
function CommandForm({ draft, onChange, problems, toolNames, busy, isNew, onSave, onDelete, onCancel }) {
  const { t, i18n } = useTranslation('workspace')
  const { t: tc } = useTranslation('chat')
  const set = (patch) => onChange({ ...draft, ...patch })
  const setTemplate = (template) => onChange(withArgsFromTemplate({ ...draft, template }))
  const setDefault = (i, value) => onChange({ ...draft, args: draft.args.map((a, j) => (j === i ? { ...a, default: value } : a)) })
  const has = (code) => problems.includes(code)
  const args = draft.args
  const sample = draft.name ? `/${draft.name}${args.length ? ` ${args.map(a => String(a.default || a.name)).join(' ')}` : ''}` : '/name'
  const expands = draft.type === 'tool'
    ? expandCommand({ type: 'tool', tool: draft.tool || '…' }, '', { toolText: (tool) => tc('slash.userTool', { tool }) })
    : expandCommand({ type: 'prompt', template: draft.template, args: args.map(a => ({ name: a.name, default: a.default === '' ? `{{${a.name}}}` : a.default })) }, '')
  const toolKnown = !draft.tool || toolNames.length === 0 || toolNames.includes(draft.tool.trim())
  const err = (...codes) => codes.filter(has).map(c => t(`commands.problem.${c}`, { max: COMMAND_LIMITS.description, template: COMMAND_LIMITS.template, args: COMMAND_LIMITS.args }))
  const nameErrs = err('nameEmpty', 'nameFormat', 'nameBuiltin', 'nameTaken')
  // A new command that nobody has touched yet is not judged.
  const pristine = isNew && !draft.name && !draft.description && !draft.template && !draft.tool
  const showNameErr = nameErrs.length > 0 && !pristine && (draft.name !== '' || !isNew)

  return (
    <form className="cm-form" onSubmit={e => { e.preventDefault(); onSave() }} data-testid="cm-form" aria-label={t('commands.form')}>
      <div className="cm-form__top">
        <div className="dk-field cm-form__name">
          <label className="dk-label" htmlFor="cm-name">{t('commands.field.name')}</label>
          <div className="cm-slash">
            <span className="cm-slash__mark dk-mono" aria-hidden="true">/</span>
            <input
              id="cm-name" className="dk-input dk-mono" value={draft.name} maxLength={32} spellCheck={false} autoComplete="off"
              aria-invalid={showNameErr ? 'true' : undefined} aria-describedby="cm-name-hint"
              onChange={e => set({ name: e.target.value.replace(/^\/+/, '') })} data-testid="cm-name"
            />
          </div>
          {showNameErr
            ? <p className="dk-field-error" id="cm-name-hint" data-testid="cm-name-error">{nameErrs[0]}</p>
            : <p className="dk-hint" id="cm-name-hint">{t('commands.nameHint')}</p>}
        </div>
        <div className="dk-field">
          <span className="dk-label" id="cm-type-label">{t('commands.field.what')}</span>
          <Segmented
            label={t('commands.field.what')} value={draft.type} itemTestId="cm-type-"
            options={[{ id: 'prompt', label: t('commands.type.prompt') }, { id: 'tool', label: t('commands.type.tool') }]}
            onChange={type => set({ type })}
          />
        </div>
      </div>

      <div className="dk-field">
        <label className="dk-label" htmlFor="cm-desc">{t('commands.field.description')}</label>
        <input
          id="cm-desc" className="dk-input" value={draft.description} maxLength={COMMAND_LIMITS.description}
          onChange={e => set({ description: e.target.value })} data-testid="cm-description"
        />
      </div>

      {draft.type === 'prompt' ? (
        <>
          <div className="dk-field">
            <label className="dk-label" htmlFor="cm-template">{t('commands.field.template')}</label>
            <textarea
              id="cm-template" className="dk-input dk-textarea cm-template" rows={4} value={draft.template} maxLength={COMMAND_LIMITS.template}
              aria-describedby="cm-template-hint" onChange={e => setTemplate(e.target.value)} data-testid="cm-template"
            />
            <p className="dk-hint" id="cm-template-hint">{t('commands.templateHint', { example: '{{name}}', interpolation: { escapeValue: false } })}</p>
          </div>
          <div className="dk-field" data-testid="cm-args">
            <span className="dk-label">{t('commands.field.args')}</span>
            {args.length === 0 ? (
              <p className="dk-hint" data-testid="cm-args-none">{t('commands.noArgs')}</p>
            ) : (
              <ul className="cm-args">
                {args.map((a, idx) => {
                  const numeric = a.default !== '' && !Number.isNaN(Number(a.default))
                  return (
                    <li key={a.name} className="cm-arg" data-testid={`cm-arg-${a.name}`}>
                      <code className="cm-arg__name">{a.name}</code>
                      <span className="cm-arg__kind">{t(`commands.argKind.${numeric ? 'number' : 'text'}`)}</span>
                      <label className="cm-arg__default" htmlFor={`cm-arg-${a.name}-default`}>{t('commands.argDefault')}</label>
                      <input
                        id={`cm-arg-${a.name}-default`} className="dk-input cm-arg__input" value={String(a.default)} maxLength={COMMAND_LIMITS.default}
                        aria-invalid={String(a.default) === '' ? 'true' : undefined}
                        onChange={e => setDefault(idx, e.target.value)} data-testid={`cm-arg-${a.name}-default`}
                      />
                    </li>
                  )
                })}
              </ul>
            )}
            {args.length > 0 && <p className="dk-hint">{t('commands.argsHint', { example: sample })}</p>}
          </div>
        </>
      ) : (
        <div className="dk-field">
          <label className="dk-label" htmlFor="cm-tool">{t('commands.field.tool')}</label>
          <input
            id="cm-tool" className="dk-input dk-mono" value={draft.tool} list="cm-tools" maxLength={COMMAND_LIMITS.tool} spellCheck={false} autoComplete="off"
            aria-describedby="cm-tool-hint" onChange={e => set({ tool: e.target.value })} data-testid="cm-tool"
          />
          <datalist id="cm-tools">{toolNames.map(n => <option key={n} value={n} />)}</datalist>
          <p className="dk-hint" id="cm-tool-hint">{t('commands.toolHint')}</p>
          {!toolKnown && <p className="dk-hint cm-warn" data-testid="cm-tool-unknown"><Icon name="warning" /> {t('commands.toolUnknown')}</p>}
        </div>
      )}

      {(draft.type === 'tool' || draft.template.trim()) && (
        <div className="cm-expands" data-testid="cm-expands">
          <span className="cm-expands__label">{t('commands.expands')}</span>
          <p>{expands}</p>
        </div>
      )}

      <div className="cm-form__foot">
        {pristine ? <span /> : problems.length === 0
          ? <StatusChip tone="ok" icon="check" testId="cm-valid">{t('commands.valid')}</StatusChip>
          : <StatusChip tone="warn" icon="warning" testId="cm-invalid">{t('commands.invalid')}</StatusChip>}
        <span className="cm-form__acts">
          {!isNew && <Button variant="danger-ghost" size="sm" icon="trash" disabled={busy} onClick={onDelete} data-testid="cm-delete">{t('commands.delete')}</Button>}
          {isNew && <Button variant="ghost" size="sm" disabled={busy} onClick={onCancel} data-testid="cm-cancel">{t('wellKnown.cancel')}</Button>}
          <Button type="submit" variant="primary" size="sm" busy={busy} disabled={problems.length > 0} data-testid="cm-save">{t('commands.save')}</Button>
        </span>
      </div>
      {problems.length > 0 && !pristine && (
        <ul className="cm-problems" data-testid="cm-problems">
          {err(...problems.filter(c => !['nameEmpty', 'nameFormat', 'nameBuiltin', 'nameTaken'].includes(c) || !showNameErr)).map(m => <li key={m}>{m}</li>)}
        </ul>
      )}
    </form>
  )
}

// CommandsEditor edits commands.json: the user's slash commands. A command is
// a prompt template or a named tool; it only fills the message box. The list
// is saved when a command is saved or deleted, merged by name with the file as
// it is then, so a change the assistant made meanwhile is not lost.
export default function CommandsEditor() {
  const { t, i18n } = useTranslation('workspace')
  const { t: tc } = useTranslation('chat')
  const wk = useWellKnown('commands')
  const { doc } = wk
  const toolNames = useToolNames()
  const list = useMemo(() => fileList(doc), [doc])
  const [sel, setSel] = useState(null)
  const [draft, setDraft] = useState(null)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState(null)
  const [savedAt, setSavedAt] = useState(null)
  const [sourceOpen, setSourceOpen] = useState(false)
  const [source, setSource] = useState('')

  const others = useMemo(() => list.filter(c => c.name !== sel), [list, sel])
  const problems = useMemo(() => (draft ? commandProblems(draft, others) : []), [draft, others])

  const open = (c) => { setSel(c.name); setDraft(toDraft(c)); setProblem(null) }
  const startNew = () => { setSel(NEW); setDraft(blank()); setProblem(null) }
  const close = () => { setSel(null); setDraft(null) }

  // The first command opens by itself, so the form is never an empty page.
  useEffect(() => {
    if (sel === null && list.length > 0 && !draft) open(list[0])
    // open only reads its argument.
  }, [list]) // eslint-disable-line react-hooks/exhaustive-deps

  const write = useCallback(async (build) => {
    setBusy(true)
    setProblem(null)
    try {
      let version = doc?.version ?? 0
      let theirs = list
      let saved
      for (let i = 0; ; i++) {
        try {
          saved = await wk.save({ commands: cleanCommands(build(theirs)) }, { version })
          break
        } catch (e) {
          // The file changed meanwhile: build the list again on the newer one.
          if (!e.conflict || i >= 3) throw e
          theirs = fileList(e.current)
          version = e.current.version ?? 0
        }
      }
      setSavedAt(new Date())
      return saved
    } catch (e) {
      setProblem(e.conflict ? { message: t('wellKnown.conflict'), errors: [] } : wellKnownProblem(e, t))
      return null
    } finally { setBusy(false) }
  }, [wk, doc, list, t])

  const save = async () => {
    if (!draft || problems.length > 0) return
    const mine = { ...draft, name: draft.name.trim() }
    const was = sel
    const saved = await write((base) => {
      const next = base.map(c => ({ ...c }))
      const at = next.findIndex(c => c.name === was)
      if (was !== NEW && at >= 0) next[at] = mine
      else next.push(mine)
      return next
    })
    if (saved) { setSel(mine.name); setDraft(toDraft(fileList(saved).find(c => c.name === mine.name) || mine)) }
  }
  const remove = async () => {
    const was = sel
    const saved = await write(base => base.filter(c => c.name !== was))
    if (saved) {
      const rest = fileList(saved)
      if (rest.length > 0) open(rest[0])
      else close()
    }
  }
  const revert = async () => {
    setBusy(true)
    setProblem(null)
    try { await wk.revert(); setSavedAt(new Date()); close() } catch (e) { setProblem(wellKnownProblem(e, t)) } finally { setBusy(false) }
  }
  const toggleSource = () => {
    if (!sourceOpen) setSource(sourceText(doc?.data))
    setSourceOpen(v => !v)
  }
  const saveSource = async () => {
    const parsed = parseSource(source)
    if (parsed.error) { setProblem({ message: t(`wellKnown.source.${parsed.error}`), errors: parsed.detail ? [parsed.detail] : [] }); return }
    setBusy(true)
    setProblem(null)
    try {
      await wk.save(parsed.value, { replace: true, version: doc?.version ?? 0 })
      setSourceOpen(false)
      setSavedAt(new Date())
      close()
    } catch (e) { setProblem(e.conflict ? { message: t('wellKnown.conflict'), errors: [] } : wellKnownProblem(e, t)) } finally { setBusy(false) }
  }

  // The menu preview: the saved commands, with the one being edited in place.
  const previewRows = useMemo(() => {
    const rows = list.map(c => ({ key: c.name, name: c.name, description: c.description || '', type: c.type }))
    if (draft && sel === NEW) rows.push({ key: NEW, name: draft.name, description: draft.description, type: draft.type })
    else if (draft) {
      const at = rows.findIndex(r => r.name === sel)
      if (at >= 0) rows[at] = { key: sel, name: draft.name, description: draft.description, type: draft.type }
    }
    return rows
  }, [list, draft, sel])
  const builtin = useMemo(() => [
    { cmd: '/model', icon: 'cube', desc: tc('slash.model.desc') },
    { cmd: '/clear', icon: 'eraser', desc: tc('slash.clear.desc') },
  ], [tc])

  const time = savedAt ? (() => { try { return savedAt.toLocaleTimeString(i18n.language, { hour: 'numeric', minute: '2-digit' }) } catch { return '' } })() : ''

  return (
    <section className="wk-card cm" aria-labelledby="cm-title" data-testid="wk-card-commands">
      <header className="wk-card__head">
        <div className="wk-card__titles">
          <h2 id="cm-title">{t('commands.title')}</h2>
          <p>{t('commands.lede')}</p>
        </div>
        <Button size="sm" icon="plus" onClick={startNew} disabled={busy || list.length >= COMMAND_LIMITS.commands || sel === NEW} data-testid="cm-new">{t('commands.new')}</Button>
      </header>

      {doc && !doc.valid && (
        <div className="wk-note wk-note--warn" role="status" data-testid="wk-invalid-commands">
          <Icon name="warning" />
          <div><p>{doc.fallback ? t('wellKnown.invalidFallback') : t('wellKnown.invalid')}</p><ProblemDetails errors={doc.errors} /></div>
        </div>
      )}
      {wk.error && !doc && (
        <div className="wk-note wk-note--error" role="alert"><Icon name="alert-circle" /><div><p>{t('wellKnown.loadFailed')}</p><ProblemDetails errors={[wk.error.message]} /></div></div>
      )}

      {!doc && !wk.error ? <div className="wk-loading"><LoadingSpinner size="sm" /></div> : (
        <div className="cm__body">
          <div className="cm__main">
            {sourceOpen ? (
              <div className="dk-field">
                <label className="dk-label" htmlFor="cm-source">{t('wellKnown.source.label')}</label>
                <textarea id="cm-source" className="dk-input dk-textarea dk-mono wk-source" spellCheck={false} value={source} onChange={e => { setSource(e.target.value); setProblem(null) }} data-testid="wk-source-text-commands" />
                <p className="dk-hint">{t('wellKnown.source.hint')}</p>
                <div className="wk-actions">
                  <Button variant="primary" size="sm" icon="save" busy={busy} onClick={saveSource} data-testid="cm-source-save">{t('wellKnown.save')}</Button>
                  <Button variant="ghost" size="sm" onClick={toggleSource}>{t('wellKnown.cancel')}</Button>
                </div>
              </div>
            ) : (
              <>
                {list.length === 0 && sel !== NEW && (
                  <p className="cm-empty" data-testid="cm-empty">{t('commands.empty')}</p>
                )}
                <ul className="cm-list" data-testid="cm-list">
                  {list.map(c => (
                    <li key={c.name}>
                      <button type="button" className="cm-item" aria-pressed={sel === c.name} onClick={() => open(c)} data-testid={`cm-item-${c.name}`}>
                        <code className="cm-item__name">/{c.name}</code>
                        <span className="cm-item__desc">{c.description}</span>
                        <span className="dk-badge cm-badge">{tc(`slash.badge.${c.type}`)}</span>
                      </button>
                    </li>
                  ))}
                </ul>
                {draft && (
                  <CommandForm
                    draft={draft} onChange={setDraft} problems={problems} toolNames={toolNames} busy={busy} isNew={sel === NEW}
                    onSave={save} onDelete={remove} onCancel={() => { if (list.length > 0) open(list[0]); else close() }}
                  />
                )}
              </>
            )}
            {problem && (
              <div className="wk-note wk-note--error" role="alert" data-testid="wk-problem-commands">
                <Icon name="alert-circle" />
                <div><p>{problem.message}</p><ProblemDetails errors={problem.errors} /></div>
              </div>
            )}
          </div>
          <MenuPreview rows={previewRows} builtin={builtin} selected={draft?.name} />
        </div>
      )}

      <FileFooter
        name="commands" file={WELL_KNOWN_FILES.commands} onSource={toggleSource} sourceOpen={sourceOpen}
        onRevert={revert} canRevert={!busy && !!doc?.exists}
      >
        {savedAt && <span className="wk-stored__saved" role="status" data-testid="cm-saved">{t('wellKnown.savedAt', { time })}</span>}
      </FileFooter>
    </section>
  )
}
