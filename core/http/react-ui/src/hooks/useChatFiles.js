import { useEffect, useState } from 'react'
import { chatsApi } from '../utils/api'
import { isWorkingFile } from '../utils/previewPane'

const EMPTY = { files: [], folder: '', folderPath: '', loaded: false, error: '' }

// The files of one chat, read again whenever tick changes (a files event of
// the chat). The server builds the list from the chat's transcript and its
// history of changes, so it is the same list the model gets in its prompt.
export function useChatFiles(chatId, tick) {
  const [state, setState] = useState(EMPTY)
  useEffect(() => {
    if (!chatId) {
      setState(EMPTY)
      return undefined
    }
    // An answer for a chat that is no longer open must not show.
    let current = true
    chatsApi.files(chatId)
      .then((d) => {
        if (!current) return
        setState({ files: d.files || [], folder: d.folder || '', folderPath: d.folder_path || '', loaded: true, error: '' })
      })
      .catch((err) => { if (current) setState(s => ({ ...s, loaded: true, error: err?.message || 'error' })) })
    return () => { current = false }
  }, [chatId, tick])
  // A new chat starts empty, not with the files of the one before.
  useEffect(() => { setState(EMPTY) }, [chatId])
  return state
}

// The groups of the files pane: what the user attached, what the assistant
// created, the files it changed that it did not create, and its working files
// (scripts, logs, drafts), which the pane folds away under the rest.
export function groupChatFiles(files) {
  const groups = { uploaded: [], created: [], changed: [], working: [] }
  for (const f of files) {
    if (f.origin === 'user') groups.uploaded.push(f)
    else if (isWorkingFile(f)) groups.working.push(f)
    else if (f.changed) groups.changed.push(f)
    else groups.created.push(f)
  }
  return groups
}
