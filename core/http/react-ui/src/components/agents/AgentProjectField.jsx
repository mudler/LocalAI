/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import ProjectSheet from '../projects/ProjectSheet'
import { CREATE_PROJECT, projectOptions } from '../../utils/agentProject'

// The Project field of an agent: one of the person's active projects, a new
// project made on the spot, or no project. The create wizard, the editor and
// the move dialog of an existing agent all draw it.
//   list       the result of useActiveProjects (the page holds it)
//   value      the project id, or '' for no project
//   onChange   (id, project?) => void; project is set for one made on the spot
//   disabled   true while the field cannot change
//   id         the DOM id of the select
//   help       the text under the field (a default is drawn)
//   allowNone  false when the agent must stay in a project (a move target)
export default function AgentProjectField({ list, value, onChange, disabled = false, id = 'ag-project', help, allowNone = true }) {
  const { t } = useTranslation('agents')
  const [creating, setCreating] = useState(false)
  const options = projectOptions(list.all, value)
  const archived = (p) => p.status !== 'active'
  const pick = (v) => {
    if (v === CREATE_PROJECT) { setCreating(true); return }
    onChange(v, options.find((p) => p.id === v))
  }
  return (
    <div className="dk-field" data-testid="agent-project-field">
      <label className="dk-label" htmlFor={id}>{t('projectField.label')}</label>
      <select
        id={id} className="dk-input" value={value || ''} disabled={disabled}
        aria-describedby={`${id}-help`}
        onChange={(e) => pick(e.target.value)}
        data-testid="agent-project-select"
      >
        {allowNone && <option value="">{t('projectField.none')}</option>}
        {!allowNone && !value && <option value="" disabled>{t('projectField.choose')}</option>}
        {options.map((p) => <option key={p.id} value={p.id}>{archived(p) ? t('projectField.archivedName', { name: p.name }) : p.name}</option>)}
        <option value={CREATE_PROJECT}>{t('projectField.create')}</option>
      </select>
      <p className="ag-note" id={`${id}-help`}>{help || t('projectField.help')}</p>
      {creating && (
        // The sheet is a portal, so its form would submit the form this field
        // sits in (the agent editor): the wrapper stops that.
        <div onSubmit={(e) => e.stopPropagation()}>
          <ProjectSheet
            onClose={() => setCreating(false)}
            onSaved={(p) => { setCreating(false); list.add(p); onChange(p.id, p) }}
          />
        </div>
      )}
    </div>
  )
}
