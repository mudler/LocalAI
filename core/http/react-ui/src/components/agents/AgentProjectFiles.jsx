/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import './agentparts.css'

// The Files section of an agent created from a project. The agent works only
// in the two folders of its project, so there is no folder to choose: the
// project files with a mode the person picks, and the project memory, which is
// always read only (a note is saved by proposing a change the person confirms).
//   project   the project, or null when it no longer exists
//   mode      read or write
//   onMode    called with the new mode
export default function AgentProjectFiles({ project, mode, onMode }) {
  const { t } = useTranslation('agents')
  const name = project?.name || ''
  return (
    <div className="ag-pfolders" data-testid="agent-project-files">
      <div className="ag-pfolder" data-testid="agent-project-files-row">
        <h4 className="ag-pfolder__title"><Icon name="folder" />{t('project.files')}</h4>
        <div className="form-group">
          <label className="form-label" htmlFor="ag-project-files-mode">{t('project.filesMode')}</label>
          <select id="ag-project-files-mode" className="input" value={mode === 'read' ? 'read' : 'write'} onChange={(e) => onMode(e.target.value)} data-testid="agent-project-files-mode">
            <option value="write">{t('project.canEdit')}</option>
            <option value="read">{t('project.readOnly')}</option>
          </select>
        </div>
        <p className="ag-note">{name ? t('project.filesHelp', { name }) : t('project.filesHelpNoName')}</p>
      </div>
      <div className="ag-pfolder" data-testid="agent-project-memory-row">
        <h4 className="ag-pfolder__title"><Icon name="database" />{t('project.memory')}</h4>
        <p className="ag-pfolder__fixed" data-testid="agent-project-memory-mode"><Icon name="lock" />{t('project.readOnly')}</p>
        <p className="ag-note">{t('project.memoryHelp')}</p>
      </div>
    </div>
  )
}
