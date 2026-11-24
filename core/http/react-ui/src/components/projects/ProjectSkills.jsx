/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import ConfirmDialog from '../ConfirmDialog'
import { Button, Callout, LoadError, SkeletonRows } from '../ui'
import { chatSkillsApi } from '../../utils/api'
import { projectSkillPath } from '../../utils/projects'

// The skills that live in the project folder (Projects/<slug>/skills): they are
// on in every chat and agent of the project and in no other chat. Create, edit
// and delete use the skill editor. While the folder has other writers the
// server leaves the layer out, and the page says so.
export default function ProjectSkills({ project, archived = false, addToast }) {
  const { t } = useTranslation(['projects', 'common'])
  const [state, setState] = useState('loading') // loading | ready | error
  const [error, setError] = useState(null)
  const [skills, setSkills] = useState([])
  const [removing, setRemoving] = useState(null)
  const blocked = project.skills_blocked === true

  const load = useCallback(async () => {
    setState('loading')
    try {
      const res = await chatSkillsApi.list(null, { project: project.id })
      setSkills((res?.skills || []).filter(s => s.layer === 'project' && !s.invalid))
      setState('ready')
    } catch (err) {
      setError(err)
      setState('error')
    }
  }, [project.id])
  useEffect(() => { load() }, [load])

  const remove = async () => {
    const skill = removing
    setRemoving(null)
    try {
      await chatSkillsApi.remove('project', skill.name, { project: project.id })
      setSkills(list => list.filter(s => s.name !== skill.name))
      addToast?.(t('projectSkills.removed', { name: skill.name }), 'success')
    } catch (err) {
      addToast?.(t('projectSkills.removeFailed', { message: err.message || String(err) }), 'error')
    }
  }

  return (
    <div className="pj-stack" data-testid="project-skills">
      <section className="pj-section">
        <div className="pj-head">
          <h2 className="pj-h2">{t('projectSkills.title')}</h2>
          <Button size="sm" variant="primary" icon="plus" to={projectSkillPath(project)} disabled={archived || blocked} data-testid="project-skill-new">{t('projectSkills.new')}</Button>
        </div>
        <p className="pj-note">{t('projectSkills.body')}</p>
        {blocked && <Callout tone="warn" testId="project-skills-blocked">{t('projectSkills.blocked')}</Callout>}
        {state === 'loading' && <SkeletonRows count={2} />}
        {state === 'error' && <LoadError title={t('projectSkills.loadFailed')} error={error} onRetry={load} />}
        {state === 'ready' && skills.length === 0 && <p className="pj-muted" data-testid="project-skills-empty">{t('projectSkills.empty')}</p>}
        {state === 'ready' && skills.length > 0 && (
          <ul className="pj-list" data-testid="project-skill-list">
            {skills.map(s => (
              <li key={s.name} className="pj-line" data-testid="project-skill" data-skill={s.name}>
                <span className="pj-row__main">
                  <span className="pj-mono">{s.name}</span>
                  {s.description && <span className="pj-muted">{s.description}</span>}
                </span>
                {!archived && (
                  <span className="pj-acts">
                    <Button size="sm" icon="edit" to={projectSkillPath(project, s.name)} data-testid="project-skill-edit" aria-label={t('projectSkills.edit', { name: s.name })}>{t('projectSkills.editLabel')}</Button>
                    <Button size="sm" variant="ghost" icon="trash" onClick={() => setRemoving(s)} data-testid="project-skill-delete" aria-label={t('projectSkills.delete', { name: s.name })}>{t('projectSkills.deleteLabel')}</Button>
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
      <ConfirmDialog
        open={Boolean(removing)}
        danger
        title={t('projectSkills.deleteTitle', { name: removing?.name || '' })}
        message={t('projectSkills.deleteBody')}
        confirmLabel={t('projectSkills.deleteLabel')}
        cancelLabel={t('common:actions.cancel')}
        onConfirm={remove}
        onCancel={() => setRemoving(null)}
      />
    </div>
  )
}
