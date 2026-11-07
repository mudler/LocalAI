/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, Callout, Segmented } from '../ui'
import AgentFolderDialog from './AgentFolderDialog'
import { effectiveModes, folderState, MAX_FOLDERS } from '../../utils/chatAgents'

// The Files choice of an agent: No files, All my files or Only these folders,
// and the folder rows with their mode. Both the full editor and the new-agent
// wizard draw it, from the state of useAgentAbilities.
//   form, updateField   the form of the page
//   abilities           the result of useAgentAbilities
//   savedConfig         the agent as saved, for the notice of folders not mounted
//   isEdit              a saved agent: the change applies from the next message
export default function AgentFiles({ form, updateField, abilities: ab, savedConfig, isEdit }) {
  const { t } = useTranslation('agents')
  const rows = form.folders || []
  const effective = effectiveModes(rows)
  const shownPath = (path) => (path === '/' ? t('folder.home') : path)
  const choice = ab.filesChoice
  return (
    <>
      <Segmented
        label={t('folder.scopeLabel')} testId="agent-folder-scope" itemTestId="agent-folder-scope-"
        value={choice === 'folders' ? 'one' : choice} onChange={(c) => ab.setScope(c === 'one' ? 'folders' : c)}
        options={[{ id: 'none', label: t('folder.none') }, { id: 'all', label: t('folder.all') }, { id: 'one', label: t('folder.one') }]}
      />
      {choice === 'none' && <p className="ag-note ag-note--gap" data-testid="agent-folder-none-help">{t('folder.noneHelp')}</p>}
      {choice === 'folders' && (
        <div className="ag-folder" data-testid="agent-folder">
          {rows.map((f, i) => {
            const st = f.path ? 'ok' : folderState({ folder: f })
            const forced = f.path && effective[i] !== (f.mode === 'write' ? 'write' : 'read')
            return (
              <div className="ag-folder__item" key={i} data-testid="agent-folder-row">
                {f.path ? (
                  <div className="ag-folder__row">
                    <span className="ag-folder__path" data-testid="agent-folder-path"><Icon name="folder" />{shownPath(f.path)}</span>
                    <Button size="sm" onClick={() => ab.setChoosingFolder(i)} data-testid="agent-folder-change">{t('folder.change')}</Button>
                    <Button size="sm" variant="ghost" icon="close" onClick={() => ab.removeRow(i)} aria-label={t('folder.remove', { path: shownPath(f.path) })} data-testid="agent-folder-remove" />
                  </div>
                ) : st === 'missing' || st === 'unset' ? (
                  <Callout
                    tone="error" role="alert" testId="agent-folder-missing"
                    actions={(
                      <>
                        <Button size="sm" icon="folder" onClick={() => ab.setChoosingFolder(i)}>{t('folder.chooseOther')}</Button>
                        <Button size="sm" variant="ghost" onClick={() => ab.removeRow(i)} data-testid="agent-folder-remove">{t('folder.removeRow')}</Button>
                      </>
                    )}
                  >
                    {t(st === 'missing' ? 'folder.missing' : 'folder.unset')}
                  </Callout>
                ) : (
                  <div className="ag-folder__row">
                    <Button size="sm" icon="folder" onClick={() => ab.setChoosingFolder(i)} data-testid="agent-folder-choose">{t('folder.choose')}</Button>
                    <Button size="sm" variant="ghost" icon="close" onClick={() => ab.removeRow(i)} aria-label={t('folder.removeRow')} data-testid="agent-folder-remove" />
                  </div>
                )}
                <div className="form-group">
                  <span className="form-label">{t('folder.modeLabel')}</span>
                  <Segmented
                    label={t('folder.modeLabel')} testId={`agent-folder-mode-${i}`} itemTestId={`agent-folder-mode-${i}-`}
                    value={f.mode === 'write' ? 'write' : 'read'} onChange={(mode) => ab.setRow(i, { mode })}
                    options={[{ id: 'read', label: t('folder.read') }, { id: 'write', label: t('folder.write') }]}
                  />
                  {forced && <p className="ag-note" data-testid="agent-folder-effective">{t(effective[i] === 'write' ? 'folder.effectiveWrite' : 'folder.effectiveRead')}</p>}
                </div>
              </div>
            )
          })}
          <div className="ag-folder__row">
            <Button size="sm" icon="plus" onClick={ab.addRow} disabled={rows.length >= MAX_FOLDERS} data-testid="agent-folder-add">{t('folder.add')}</Button>
            <span className="ag-note" data-testid="agent-folder-count">{t('folder.count', { n: rows.length, max: MAX_FOLDERS })}</span>
          </div>
          {savedConfig?.folder_notice?.not_mounted > 0 && (
            <p className="ag-note" data-testid="agent-folder-notice">{t('folder.notMounted', { n: savedConfig.folder_notice.not_mounted })}</p>
          )}
          <p className="ag-note">{t('folder.help')}</p>
          <p className="ag-note">{t('folder.commands')}</p>
          {isEdit && <p className="ag-note">{t('folder.nextMessage')}</p>}
        </div>
      )}
      {choice === 'all' && <p className="ag-note ag-note--gap">{t('folder.allHelp')}</p>}
      {ab.choosingFolder !== null && (
        <AgentFolderDialog
          start={rows[ab.choosingFolder === 'new' ? rows.length - 1 : ab.choosingFolder]?.path || '/'}
          onCancel={() => {
            // A row that was just added and never chosen goes away again.
            if (ab.choosingFolder === 'new') ab.removeRow(rows.length - 1)
            ab.setChoosingFolder(null)
          }}
          onChoose={(path) => {
            const at = ab.choosingFolder === 'new' ? rows.length - 1 : ab.choosingFolder
            ab.setChoosingFolder(null)
            ab.setRow(at, { path, state: 'ok' })
          }}
        />
      )}
    </>
  )
}
