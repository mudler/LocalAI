/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
import Icon from '../components/Icon'
import { useAuth } from '../context/AuthContext'
import { chatSkillsApi } from '../utils/api'
import './chatskills.css'

const GROUPS = [
  { layer: 'personal', key: 'personal' },
  { layer: 'folder', key: 'folder' },
  { layer: 'shared', key: 'shared' },
  { layer: 'builtin', key: 'builtin' },
]

const emptyForm = { layer: 'personal', name: '', description: '', body: '', isNew: true }

export default function ChatSkills() {
  const { t } = useTranslation('chats')
  const { isAdmin } = useAuth()
  const [skills, setSkills] = useState([])
  const [notes, setNotes] = useState([])
  const [off, setOff] = useState('')
  const [error, setError] = useState('')
  const [toDelete, setToDelete] = useState(null)
  const [form, setForm] = useState(null)
  const [busy, setBusy] = useState(false)

  const fail = useCallback((e) => {
    if (e?.status === 409 && e.body?.error?.type === 'files_off') { setOff(e.body.error.message); return }
    setError(e?.message || t('errors.send'))
  }, [t])

  const load = useCallback(async () => {
    try {
      const res = await chatSkillsApi.list()
      setSkills(res.skills || [])
      setNotes(res.notes || [])
      setOff('')
    } catch (e) { fail(e) }
  }, [fail])

  useEffect(() => { load() }, [load])

  const edit = async (sk) => {
    setError('')
    try {
      const d = await chatSkillsApi.get(sk.layer, sk.name)
      setForm({ layer: sk.layer, name: sk.name, description: d.description, body: d.body, isNew: false })
    } catch (e) { fail(e) }
  }

  const save = async (e) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      if (form.isNew) {
        await chatSkillsApi.create({ layer: form.layer, name: form.name.trim(), description: form.description, body: form.body })
      } else {
        await chatSkillsApi.update(form.layer, form.name, { description: form.description, body: form.body })
      }
      setForm(null)
      await load()
    } catch (err) { fail(err) } finally { setBusy(false) }
  }

  const remove = async (sk) => {
    setError('')
    try {
      await chatSkillsApi.remove(sk.layer, sk.name)
      await load()
    } catch (e) { fail(e) }
  }

  const confirmDelete = (sk) => { setToDelete(null); remove(sk) }

  if (off) {
    return (
      <div className="cs-page" data-testid="chat-skills-page">
        <h1 className="cs-title">{t('skillsPage.title')}</h1>
        <div className="cs-notice" role="note" data-testid="files-off">{off}</div>
      </div>
    )
  }

  return (
    <div className="cs-page" data-testid="chat-skills-page">
      <header className="cs-head">
        <h1 className="cs-title">{t('skillsPage.title')}</h1>
        <button type="button" className="btn btn-primary btn-sm" onClick={() => setForm(emptyForm)}>
          <Icon name="plus" /> {t('skillsPage.new')}
        </button>
      </header>
      <p className="cs-muted">{t('skillsPage.intro')}</p>

      {error && (
        <div className="cs-error" role="alert">
          <span>{error}</span>
          <button type="button" className="cs-link" onClick={() => setError('')}>{t('errors.dismiss')}</button>
        </div>
      )}
      {notes.map((n) => <p key={n} className="cs-muted" role="note">{n}</p>)}

      {form && (
        <form className="cs-form" onSubmit={save} aria-label={form.isNew ? t('skillsPage.new') : t('skillsPage.edit')}>
          {form.isNew && isAdmin && (
            <label className="cs-field">
              <span>{t('skillsPage.layer')}</span>
              <select className="input" value={form.layer} onChange={(e) => setForm({ ...form, layer: e.target.value })}>
                <option value="personal">{t('skillsPage.groups.personal')}</option>
                <option value="shared">{t('skillsPage.groups.shared')}</option>
              </select>
            </label>
          )}
          <label className="cs-field">
            <span>{t('skillsPage.name')}</span>
            <input className="input" value={form.name} disabled={!form.isNew} required pattern="[a-z0-9]+(-[a-z0-9]+)*"
              placeholder="release-notes" onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </label>
          <label className="cs-field">
            <span>{t('skillsPage.description')}</span>
            <input className="input" value={form.description} required onChange={(e) => setForm({ ...form, description: e.target.value })} />
          </label>
          <label className="cs-field">
            <span>{t('skillsPage.body')}</span>
            <textarea className="input cs-body" rows={10} value={form.body} onChange={(e) => setForm({ ...form, body: e.target.value })} />
          </label>
          <div className="cs-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy}>{t('skillsPage.save')}</button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => setForm(null)}>{t('skillsPage.cancel')}</button>
          </div>
        </form>
      )}

      {GROUPS.map(({ layer, key }) => {
        const rows = skills.filter((s) => s.layer === layer)
        if (rows.length === 0) return null
        return (
          <section key={layer} className="cs-group">
            <h2 className="cs-sub">{t(`skillsPage.groups.${key}`)}</h2>
            <ul className="cs-list">
              {rows.map((s) => (
                <li key={`${layer}-${s.name}-${s.path}`} className="cs-row" data-testid="skill-row">
                  <div className="cs-row__text">
                    <span className="cs-row__name">{s.name}</span>
                    <span className="cs-muted">{s.invalid ? t('skillsPage.invalid', { reason: s.invalid }) : s.description}</span>
                    {s.shadowed && <span className="cs-tag">{t('skillsPage.shadowed', { layer: s.shadowed })}</span>}
                  </div>
                  <span className="cs-row__actions">
                    {s.editable && (
                      <>
                        <button type="button" className="btn btn-secondary btn-sm" onClick={() => edit(s)}>{t('skillsPage.editButton')}</button>
                        <button type="button" className="btn btn-secondary btn-sm" onClick={() => setToDelete(s)}>{t('skillsPage.delete')}</button>
                      </>
                    )}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        )
      })}
      {skills.length === 0 && !form && <p className="cs-muted">{t('skillsPage.empty')}</p>}
      <p className="cs-muted"><Link to="/app/chats">{t('skillsPage.back')}</Link></p>
      <ChatDeleteConfirm item={toDelete} kind="skill" onConfirm={confirmDelete} onCancel={() => setToDelete(null)} />
    </div>
  )
}
