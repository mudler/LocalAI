// SPDX-License-Identifier: MIT
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import NameList from '../ChatNameList'
import { memoryValidation, normalizeAgentMemory } from '../../utils/chatAgents'

export default function AgentMemorySettings({ section, form, suggestions, onChange }) {
  const { t } = useTranslation('agents')
  const { collection_permissions: permissions, memory } = normalizeAgentMemory(form)
  const grants = permissions.grants
  const setGrants = next => onChange({ collection_permissions: { version: 1, grants: next }, collections: next.filter(g => g.search).map(g => g.collection) })
  const setMemory = patch => onChange({ memory: { ...memory, ...patch } })
  if (section === 'Collections') return (
    <div className="stack" data-testid="agent-collection-permissions">
      <NameList label={t('create.collections')} hint={t('memory.associationHelp')} value={grants.map(g => g.collection)} suggestions={suggestions}
        onChange={names => setGrants(names.map(collection => grants.find(g => g.collection === collection) || { collection, search: true, delete_entries: false }))} testId="agent-collections" />
      {grants.map(g => (
        <fieldset className="form-group" key={g.collection}>
          <legend className="form-label">{g.collection}</legend>
          {['search', 'delete_entries'].map(key => (
            <label className="ag-grant" key={key}>
              <input type="checkbox" checked={g[key]} onChange={e => setGrants(grants.map(row => row.collection === g.collection ? { ...row, [key]: e.target.checked } : row))} />
              <span>{t(`memory.${key}`)}</span>
            </label>
          ))}
          {g.delete_entries && <p className="ag-note tone-warning">{t('memory.deleteWarning')}</p>}
        </fieldset>
      ))}
    </div>
  )
  const error = memoryValidation(form)
  return (
    <div className="stack" data-testid="agent-memory-settings">
      <div className="form-group">
        <label className="form-label" htmlFor="agent-memory-destination">{t('memory.destination')}</label>
        <select className="input" id="agent-memory-destination" value={memory.destination} aria-invalid={!!error} aria-describedby={error ? 'agent-memory-error' : undefined} onChange={e => setMemory({ destination: e.target.value })}>
          <option value="">{t('memory.noDestination')}</option>
          {memory.destination && !grants.some(g => g.collection === memory.destination) && <option value={memory.destination}>{memory.destination} ({t('create.gone')})</option>}
          {grants.map(g => <option key={g.collection} value={g.collection}>{g.collection}</option>)}
        </select>
        {error && <p id="agent-memory-error" role="alert" className="ag-note tone-warning">{t(`memory.${error}`)}</p>}
      </div>
      <label className="ag-grant"><input type="checkbox" checked={memory.add_memory} onChange={e => setMemory({ add_memory: e.target.checked })} /><span>{t('memory.addMemory')}</span></label>
      <div className="form-group">
        <label className="form-label" htmlFor="agent-memory-storage">{t('memory.toolStorage')}</label>
        <select className="input" id="agent-memory-storage" value={memory.tool_storage} onChange={e => setMemory({ tool_storage: e.target.value })}>
          {['content', 'summary'].map(mode => <option key={mode} value={mode}>{t(`memory.storage.${mode}`)}</option>)}
        </select>
      </div>
      <div className="form-group">
        <label className="form-label" htmlFor="agent-memory-auto">{t('memory.automaticIngestion')}</label>
        <select className="input" id="agent-memory-auto" value={memory.automatic_ingestion} onChange={e => setMemory({ automatic_ingestion: e.target.value })}>
          {['off', 'incoming_only', 'incoming_and_replies', 'summary'].map(mode => <option key={mode} value={mode}>{t(`memory.modes.${mode}`)}</option>)}
        </select>
        <p className="ag-note">{t('memory.autoHelp')}</p>
        {memory.automatic_ingestion === 'off' && memory.add_memory && <p className="ag-note">{t('memory.toolOnlyHelp')}</p>}
      </div>
    </div>
  )
}
