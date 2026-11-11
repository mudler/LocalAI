import { useState, useEffect } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useParams, useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatSkillsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'
import { estimateTokens } from '../utils/library'
import Icon from '../components/Icon'
import './library.css'
import './agents.css'

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
  const [loadError, setLoadError] = useState(null)
  const [attempt, setAttempt] = useState(0)
  const [saving, setSaving] = useState(false)
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
      // Said in place of the form, with a way to try again.
      .catch(err => { if (live) setLoadError(err) })
      .finally(() => { if (live) setLoading(false) })
    return () => { live = false }
  }, [isNew, layerParam, name, attempt])

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (!form.name.trim() || !form.description.trim()) {
      addToast(t('edit.required'), 'error')
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

  const back = <Link className="ag-back" to="/app/skills"><Icon name="arrow-left" /> {t('edit.back')}</Link>

  if (loading || loadError) {
    return (
      <Page>
        <div className="ag-home">
          {back}
          {loadError
            ? <LoadError title={t('edit.loadFailed')} error={loadError} onRetry={() => { setLoadError(null); setLoading(true); setAttempt(n => n + 1) }} />
            : <SkeletonRows count={3} />}
        </div>
      </Page>
    )
  }

  const nameBad = isNew && form.name !== '' && !new RegExp(`^${NAME_PATTERN}$`).test(form.name)

  return (
    <Page>
      <div className="ag-edit__head">
        {back}
        <PageHeader title={isNew ? t('edit.titleNew') : t('edit.titleEdit', { name })} />
      </div>

      {!editable && <Callout tone="warn" testId="skill-readonly">{t('edit.readOnly')}</Callout>}
      <form className="lib-edit" onSubmit={handleSubmit} noValidate>
        <fieldset className="lib-edit__set" disabled={!editable}>
          <section className="dk-card lib-edit__card">
            <h2>{t('edit.basic')}</h2>
            {isNew && isAdmin && (
              <div className="lib-edit__field">
                <label className="dk-label" htmlFor="skill-layer">{t('edit.layer')}</label>
                <span className="dk-select-wrap">
                  <select id="skill-layer" className="dk-select" value={form.layer} onChange={(e) => setForm((f) => ({ ...f, layer: e.target.value }))}>
                    <option value="personal">{t('layers.personal')}</option>
                    <option value="shared">{t('layers.shared')}</option>
                  </select>
                </span>
                <p className="lib-edit__hint">{t('edit.layerHelp')}</p>
              </div>
            )}
            {!isNew && (
              <div className="lib-edit__field">
                <span className="dk-label">{t('edit.layer')}</span>
                <p className="lib-fine" data-testid="skill-edit-layer">{t(`layers.${form.layer}`)}</p>
              </div>
            )}
            <div className="lib-edit__field">
              <label className="dk-label" htmlFor="skill-name">{t('edit.name')} <span aria-hidden="true">*</span></label>
              <input
                id="skill-name"
                type="text"
                className="dk-input dk-input--mono"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                required
                aria-invalid={nameBad || undefined}
                aria-describedby="skill-name-hint"
                pattern={NAME_PATTERN}
                disabled={!isNew}
                placeholder="my-skill"
                autoComplete="off"
                spellCheck={false}
              />
              <p id="skill-name-hint" className={`lib-edit__hint${nameBad ? ' lib-edit__hint--error' : ''}`}>
                {nameBad ? t('edit.nameInvalid') : (!isNew ? t('edit.nameLocked') : '')}
              </p>
            </div>
            <div className="lib-edit__field">
              <label className="dk-label" htmlFor="skill-desc">{t('edit.description')} <span aria-hidden="true">*</span></label>
              <textarea
                id="skill-desc"
                className="dk-textarea"
                value={form.description}
                onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                required
                maxLength={1024}
                rows={3}
              />
              <p className="lib-edit__hint"><span>{t('edit.descriptionHelp')}</span><span>{t('edit.counter', { count: form.description.length, max: 1024 })}</span></p>
            </div>
          </section>

          <section className="dk-card lib-edit__card">
            <h2>{t('edit.content')}</h2>
            <div className="lib-edit__field">
              <label className="dk-label" htmlFor="skill-content">{t('edit.body')}</label>
              <textarea
                id="skill-content"
                className="dk-textarea lib-edit__body"
                value={form.body}
                onChange={(e) => setForm((f) => ({ ...f, body: e.target.value }))}
                rows={16}
                spellCheck={false}
              />
              <p className="lib-edit__hint"><span>{t('edit.bodyHelp')}</span><span>{t('pane.tokens', { count: estimateTokens(form.body) })}</span></p>
            </div>
          </section>
        </fieldset>

        <div className="lib-edit__bar">
          <Button variant="ghost" onClick={() => navigate('/app/skills')}>{t('edit.cancel')}</Button>
          {editable && (
            <Button type="submit" variant="primary" icon="save" busy={saving}>{isNew ? t('edit.create') : t('edit.save')}</Button>
          )}
        </div>
      </form>
    </Page>
  )
}
