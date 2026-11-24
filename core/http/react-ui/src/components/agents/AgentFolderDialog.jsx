/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Button from '../ui/Button'
import FolderPicker from '../files/FolderPicker'
import { folderName, folderPickable } from '../../utils/chatAgents'

// The folder choice of the agent editor: the Files tree, where the open
// folder is the one chosen. Folders that LocalAI manages and shared folders
// cannot be chosen; all the files ("/") can.
//   start            the folder to open on
//   onChoose(path)   onCancel()
export default function AgentFolderDialog({ start, onChoose, onCancel }) {
  const { t } = useTranslation('agents')
  const [at, setAt] = useState(start || '/')
  const name = folderName(at)
  const hintOf = (f) => (f.managed ? t('folder.managedHint') : t('folder.sharedHint'))
  return (
    <Dialog
      title={t('folder.dialogTitle')}
      description={t('folder.dialogText')}
      onClose={onCancel}
      closeLabel={t('folder.cancel')}
      labelId="ag-folder-title"
      testId="agent-folder-dialog"
      foot={(
        <>
          <Button variant="ghost" onClick={onCancel}>{t('folder.cancel')}</Button>
          <Button variant="primary" icon="check" onClick={() => onChoose(at)} data-testid="agent-folder-use">
            {name ? t('folder.use', { folder: name }) : t('folder.useAll')}
          </Button>
        </>
      )}
    >
      <FolderPicker at={at} onAt={setAt} blocked={(f) => !folderPickable(f)} hintOf={hintOf} />
    </Dialog>
  )
}
