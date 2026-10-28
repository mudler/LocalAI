import { useState, useEffect } from 'react'
import { useParams, useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatSkillsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
// eslint-disable-next-line no-unused-vars
import PageHeader from '../components/PageHeader'
import Icon from '../components/Icon'
import './library.css'

const NAME_PATTERN = '[a-z0-9]+(-[a-z0-9]+)*'

// Write or change one skill: a folder with a SKILL.md. A new skill goes to your
// own layer; an admin may also write the shared one. A skill of the built-in
// layer or of a workspace folder is read here, not written.
export default function SkillEdit() {
  const { layer: layerParam, name } = useParams()
  const [search] = useSearchParams()
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('skills')
  const { isAdmin } = useAuth()
  const isNew = !name

  const [loading, setLoading] = useState(!isNew)
  const [saving, setSaving] = useState(false)
  const [activeSection, setActiveSection] = useState('basic')
  const [editable, setEditable] = useState(true)
  const [form, setForm] = useState({
    layer: search.get('layer') === 'shared' && isAdmin ? 'shared' : 'personal',
    name: '', description: '', body: '',
  })

  useEffect(() => {
    if (isNew) return undefined
    let live = true
    chatSkillsApi.get(layerParam, name)
      .then(d => {
        if (!live) return
        setForm({ layer: d.layer, name: d.name, description: d.description || '', body: d.body || '' })
        setEditable(d.editable !== false)
      })
      .catch(err => { addToast(err.message || t('edit.loadFailed'), 'error'); navigate('/app/skills') })
      .finally(() => { if (live) setLoading(false) })
    return () => { live = false }
  }, [isNew, layerParam, name, addToast, navigate, t])

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (!form.name.trim() || !form.description.trim()) {
      addToast(t('edit.required'), 'error')
      setActiveSection('basic')
      return
    }
    setSaving(true)
    try {
      if (isNew) {
        await chatSkillsApi.create({ layer: form.layer, name: form.name.trim(), description: form.description, body: form.body })
        addToast(t('edit.created'), 'success')
      } else {
        await chatSkillsApi.update(form.layer, form.name, { description: form.description, body: form.body })
        addToast(t('edit.updated'), 'success')
      }
      navigate('/app/skills')
    } catch (err) {
      addToast(err.message || t('edit.saveFailed'), 'error')
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="page page--narrow loading-center">
        <Icon name="spinner" spin className="icon-xl text-primary" />
      </div>
    )
  }

  const sections = [
    { id: 'basic', label: t('edit.basic'), icon: 'info' },
    { id: 'content', label: t('edit.content'), icon: 'file-text' },
  ]

  return (
    <div className="page page--narrow">
      <a className="skilledit-back-link" onClick={() => navigate('/app/skills')}>
        <Icon name="arrow-left" /> {t('edit.back')}
      </a>
      <PageHeader
        title={
          <>
            <Icon name="book" className="icon-before" /> {isNew ? t('edit.titleNew') : t('edit.titleEdit', { name })}
          </>
        }
      />

      <div className="card mt-md">
        <div className="skilledit-layout">
          <div className="skilledit-sidebar">
            <ul className="skilledit-sidebar-nav">
              {sections.map((s) => (
                <li
                  key={s.id}
                  className={`skilledit-sidebar-item ${activeSection === s.id ? 'active' : ''}`}
                  onClick={() => setActiveSection(s.id)}
                >
                  <Icon name={s.icon} /> {s.label}
                </li>
              ))}
            </ul>
          </div>

          <div className="skilledit-form-area">
            {!editable && <p className="lib-note lib-note--warn" role="note" data-testid="skill-readonly">{t('edit.readOnly')}</p>}
            <form onSubmit={handleSubmit} noValidate>
              <fieldset className="skilledit-set" disabled={!editable}>
                <div hidden={activeSection !== 'basic'}>
                  <h3 className="skilledit-section-title">{t('edit.basic')}</h3>
                  {isNew && isAdmin && (
                    <div className="skilledit-field">
                      <label htmlFor="skill-layer">{t('edit.layer')}</label>
                      <select id="skill-layer" className="input" value={form.layer} onChange={(e) => setForm((f) => ({ ...f, layer: e.target.value }))}>
                        <option value="personal">{t('layers.personal')}</option>
                        <option value="shared">{t('layers.shared')}</option>
                      </select>
                      <p className="help-text">{t('edit.layerHelp')}</p>
                    </div>
                  )}
                  {!isNew && (
                    <div className="skilledit-field">
                      <label>{t('edit.layer')}</label>
                      <p data-testid="skill-edit-layer">{t(`layers.${form.layer}`)}</p>
                    </div>
                  )}
                  <div className="skilledit-field">
                    <label htmlFor="skill-name">{t('edit.name')} <span className="required">*</span></label>
                    <input
                      id="skill-name"
                      type="text"
                      className="input"
                      value={form.name}
                      onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                      required
                      pattern={NAME_PATTERN}
                      disabled={!isNew}
                      placeholder="my-skill"
                    />
                    {!isNew && <p className="help-text">{t('edit.nameLocked')}</p>}
                  </div>
                  <div className="skilledit-field">
                    <label htmlFor="skill-desc">{t('edit.description')} <span className="required">*</span></label>
                    <textarea
                      id="skill-desc"
                      className="input"
                      value={form.description}
                      onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                      required
                      maxLength={1024}
                      rows={2}
                    />
                    <p className="help-text">{t('edit.descriptionHelp')}</p>
                  </div>
                </div>

                <div hidden={activeSection !== 'content'}>
                  <h3 className="skilledit-section-title">{t('edit.content')}</h3>
                  <div className="skilledit-field">
                    <label htmlFor="skill-content">{t('edit.body')}</label>
                    <textarea
                      id="skill-content"
                      className="input skilledit-body"
                      value={form.body}
                      onChange={(e) => setForm((f) => ({ ...f, body: e.target.value }))}
                      rows={14}
                    />
                  </div>
                </div>
              </fieldset>

              <div className="skilledit-form-actions">
                <button type="button" className="btn btn-secondary" onClick={() => navigate('/app/skills')}>
                  <Icon name="close" /> {t('edit.cancel')}
                </button>
                {editable && (
                  <button type="submit" className="btn btn-primary" disabled={saving}>
                    <Icon name="save" /> {saving ? t('edit.saving') : (isNew ? t('edit.create') : t('edit.save'))}
                  </button>
                )}
              </div>
            </form>
          </div>
        </div>
      </div>
    </div>
  )
}
