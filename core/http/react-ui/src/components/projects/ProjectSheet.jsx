/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import SideSheet from '../SideSheet'
import { Button } from '../ui'
import { chatProjectsApi } from '../../utils/api'
import { DESCRIPTION_CONTEXT_CHARS, NAME_COUNTER_FROM, NAME_MAX, cleanAttached, cleanDefaults } from '../../utils/projects'
import { useProjectToolSources } from '../../hooks/useProjectToolSources'
import { AttachedEditor, DefaultsEditor } from './ProjectTools'

// The sheet that makes a project (project is null) or edits one. It keeps a
// copy of the fields; Save sends them, and a refusal of the server shows under
// the form with the sheet still open.
export default function ProjectSheet({ project = null, onClose, onSaved }) {
  const { t } = useTranslation(['projects', 'common'])
  const uid = useId()
  const sources = useProjectToolSources()
  const [name, setName] = useState(project?.name || '')
  const [description, setDescription] = useState(project?.description || '')
  const [defaults, setDefaults] = useState(() => ({ ...(project?.defaults || {}) }))
  const [attached, setAttached] = useState(() => ({ ...(project?.attached || {}) }))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [touched, setTouched] = useState(false)
  const nameMissing = !name.trim()

  const save = async () => {
    setTouched(true)
    if (nameMissing || busy) return
    setBusy(true)
    setError('')
    const body = { name: name.trim(), description: description.trim(), defaults: cleanDefaults(defaults), attached: cleanAttached(attached) }
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
            maxLength={NAME_MAX}
            aria-invalid={touched && nameMissing ? 'true' : undefined}
            aria-describedby={`${uid}-name-hint${name.length >= NAME_COUNTER_FROM ? ` ${uid}-name-count` : ''}`}
            onChange={e => setName(e.target.value)}
          />
          {touched && nameMissing && <p className="dk-field-error" role="alert">{t('sheet.nameRequired')}</p>}
          <p className="dk-hint" id={`${uid}-name-hint`}>{project ? t('sheet.nameHint', { slug: project.slug }) : t('sheet.nameHintNew')}</p>
          {name.length >= NAME_COUNTER_FROM && (
            <p className="dk-hint pj-counter" id={`${uid}-name-count`} data-testid="project-name-counter" data-full={name.length >= NAME_MAX ? 'true' : undefined}>
              {t('sheet.nameCounter', { length: name.length, max: NAME_MAX })}
            </p>
          )}
        </div>
        <div className="dk-field">
          <label className="dk-label" htmlFor="project-description">{t('sheet.description')}</label>
          <textarea id="project-description" className="dk-textarea" rows={3} value={description} aria-describedby={`${uid}-desc-hint`} onChange={e => setDescription(e.target.value)} />
          <p className="dk-hint" id={`${uid}-desc-hint`} data-testid="project-description-hint">{t('sheet.descriptionHint', { limit: DESCRIPTION_CONTEXT_CHARS })}</p>
        </div>
        <AttachedEditor value={attached} onChange={setAttached} sources={sources} limit={defaults} />
        <DefaultsEditor value={defaults} onChange={setDefaults} sources={sources} />
        {error && <p className="dk-field-error" role="alert" data-testid="project-sheet-error">{error}</p>}
        <input type="submit" hidden />
      </form>
    </SideSheet>
  )
}
