/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Icon from '../Icon'
import './agentparts.css'
import { Button } from '../ui'
import { useActiveProjects } from '../../hooks/useActiveProjects'
import { CREATE_PROJECT } from '../../utils/agentProject'

// The dialog a file of a project agent opens: the agent belongs to a project,
// so the person chooses one of their active projects or makes a new one. The
// choice is sent with the file as query parameters; the file itself is not
// changed.
//   name       the project name the file carries, '' when it has none
//   agent      the name of the agent in the file, for the text
//   onImport   called with { projectId } or { create: true }
//   onCancel   closes the dialog
export default function AgentImportProject({ name, agent, busy = false, onImport, onCancel }) {
  const { t } = useTranslation('agents')
  // The list is best effort: with no list the person can still create one.
  const { active: projects } = useActiveProjects()
  const [value, setValue] = useState('')
  const [missing, setMissing] = useState(false)

  const submit = () => {
    if (!value) { setMissing(true); return }
    onImport(value === CREATE_PROJECT ? { create: true } : { projectId: value })
  }

  return (
    <Dialog
      title={t('importProject.title')}
      description={agent ? t('importProject.textNamed', { agent }) : t('importProject.text')}
      onClose={onCancel}
      pending={busy}
      closeLabel={t('importProject.cancel')}
      labelId="ag-import-project-title"
      testId="agent-import-project"
      foot={(
        <>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>{t('importProject.cancel')}</Button>
          <Button variant="primary" icon="import" busy={busy} onClick={submit} data-testid="agent-import-project-go">{t('importProject.import')}</Button>
        </>
      )}
    >
      <div className="dk-field">
        <label className="dk-label" htmlFor="ag-import-project">{t('importProject.label')}</label>
        <select
          id="ag-import-project" className="dk-input" value={value}
          aria-invalid={missing && !value ? 'true' : undefined}
          aria-describedby="ag-import-project-help"
          onChange={(e) => { setValue(e.target.value); setMissing(false) }}
          data-testid="agent-import-project-select"
        >
          <option value="">{t('importProject.choose')}</option>
          {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
          <option value={CREATE_PROJECT}>{name ? t('importProject.create', { name }) : t('importProject.createUnnamed')}</option>
        </select>
        {missing && !value
          ? <p className="ag-import-error" id="ag-import-project-help" role="alert" data-testid="agent-import-project-error"><Icon name="alert-circle" />{t('importProject.needsChoice')}</p>
          : <p className="ag-note" id="ag-import-project-help">{value === CREATE_PROJECT ? t('importProject.createHelp', { name: name || agent || '' }) : t('importProject.help')}</p>}
      </div>
    </Dialog>
  )
}
