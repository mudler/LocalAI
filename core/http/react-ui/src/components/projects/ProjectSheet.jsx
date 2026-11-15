/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import SideSheet from '../SideSheet'
import { Button } from '../ui'
import { chatProjectsApi } from '../../utils/api'
import { cleanDefaults } from '../../utils/projects'
import { DefaultsEditor } from './ProjectTools'

// The sheet that makes a project (project is null) or edits one. It keeps a
// copy of the fields; Save sends them, and a refusal of the server shows under
// the form with the sheet still open.
export default function ProjectSheet({ project = null, onClose, onSaved }) {
  const { t } = useTranslation(['projects', 'common'])
  const [name, setName] = useState(project?.name || '')
  const [description, setDescription] = useState(project?.description || '')
  const [defaults, setDefaults] = useState(() => ({ ...(project?.defaults || {}) }))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [touched, setTouched] = useState(false)
  const nameMissing = !name.trim()

  const save = async () => {
    setTouched(true)
    if (nameMissing || busy) return
    setBusy(true)
    setError('')
    const body = { name: name.trim(), description: description.trim(), defaults: cleanDefaults(defaults) }
    try {
      const saved = project ? await chatProjectsApi.update(project.id, body) : await chatProjectsApi.create(body)
      onSaved(saved)
    } catch (err) {
      setError(err.message || String(err))
      setBusy(false)
    }
  }

  return (
    <SideSheet
      title={t(project ? 'sheet.editTitle' : 'sheet.newTitle')}
      description={t(project ? 'sheet.editBody' : 'sheet.newBody')}
      testId="project-sheet"
      closeLabel={t('common:actions.close')}
      onClose={onClose}
      footer={(
        <>
          <Button onClick={onClose}>{t('sheet.cancel')}</Button>
          <Button variant="primary" busy={busy} onClick={save}>{t('sheet.save')}</Button>
        </>
      )}
    >
      <form className="pj-form" onSubmit={e => { e.preventDefault(); save() }} noValidate>
        <div className="dk-field">
          <label className="dk-label" htmlFor="project-name">{t('sheet.name')}</label>
          <input
            id="project-name"
            className="dk-input"
            value={name}
            required
            aria-invalid={touched && nameMissing ? 'true' : undefined}
            onChange={e => setName(e.target.value)}
          />
          {touched && nameMissing
            ? <p className="dk-field-error" role="alert">{t('sheet.nameRequired')}</p>
            : <p className="dk-hint">{project ? t('sheet.nameHint', { slug: project.slug }) : t('sheet.nameHintNew')}</p>}
        </div>
        <div className="dk-field">
          <label className="dk-label" htmlFor="project-description">{t('sheet.description')}</label>
          <textarea id="project-description" className="dk-textarea" rows={3} value={description} onChange={e => setDescription(e.target.value)} />
          <p className="dk-hint">{t('sheet.descriptionHint')}</p>
        </div>
        <div className="dk-field">
          <h3 className="pj-eyebrow">{t('sheet.defaults')}</h3>
          <DefaultsEditor value={defaults} onChange={setDefaults} />
          <p className="dk-hint">{t('sheet.defaultsHint')}</p>
        </div>
        {error && <p className="dk-field-error" role="alert" data-testid="project-sheet-error">{error}</p>}
        <input type="submit" hidden />
      </form>
    </SideSheet>
  )
}
