/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatsApi } from '../utils/api'
import { chatFromExport, chatMarkdown, downloadFrom, menuChats } from '../utils/chatThread'
import { copyToClipboard } from '../utils/clipboard'
import { CAP_CHAT } from '../utils/capabilities'
import { useModels } from '../hooks/useModels'
import { CHAT_UNDO_MS, useChatDelete } from '../hooks/useChatDelete'
import { announceChats } from '../hooks/useRecentChats'
import ChatsList from '../components/ChatsList'
import ConfirmDialog from '../components/ConfirmDialog'
import HomeUndoToast from '../components/home/HomeUndoToast'
import { Button, EmptyState, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'
import './chat.css'

// All chats: the chat history on a page of its own, reached from Home and
// from the sidebar's recent chats. It is the list of the Ctrl+K menu of a
// transcript (ChatsList): search over titles and message text, rename, copy,
// export, delete with Undo and Clear all, plus Import. A row opens its
// transcript; New chat opens Home, where a chat starts.
export default function AllChats() {
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('chat')
  const { t: tc } = useTranslation('chats')
  const { models: chatModels } = useModels(CAP_CHAT)
  const [state, setState] = useState({ status: 'loading', chats: [], error: null })
  const [confirmDialog, setConfirmDialog] = useState(null)
  const importInputRef = useRef(null)

  const load = useCallback(async (keep = false) => {
    if (!keep) setState(s => ({ ...s, status: 'loading', error: null }))
    try {
      const data = await chatsApi.list(200, 'chat')
      setState({ status: 'ready', chats: (data.conversations || []).filter(c => c.kind !== 'subagent'), error: null })
    } catch (error) {
      // A failed refresh keeps the list on screen; a failed first load says so.
      setState(s => (keep && s.status === 'ready' ? s : { status: 'error', chats: [], error }))
    }
  }, [])
  useEffect(() => { load() }, [load])
  const refresh = useCallback(() => load(true), [load])

  const deletes = useChatDelete({
    remove: (id) => chatsApi.delete(id).finally(refresh),
    onFailed: (e) => addToast(e.message || tc('errors.delete'), 'error'),
  })

  const visibleChats = useMemo(
    () => menuChats(state.chats).filter(c => c.id !== deletes.pending?.id),
    [state.chats, deletes.pending],
  )
  // The sidebar's recent chats follow this list.
  useEffect(() => { if (state.status === 'ready') announceChats(visibleChats) }, [visibleChats, state.status])

  const rename = async (id, title) => {
    try {
      await chatsApi.update(id, { title })
    } catch (e) {
      addToast(e.message, 'error')
    }
    refresh()
  }

  const copyChat = async (c) => {
    try {
      const { messages } = await chatsApi.messages(c.id)
      const server = state.chats.find(x => x.id === c.id) || { title: c.name, model: c.model }
      const ok = await copyToClipboard(chatMarkdown(server, messages || []))
      addToast(ok ? t('toasts.chatCopied') : t('toasts.copyFailed'), ok ? 'success' : 'error', ok ? 2000 : 3000)
    } catch (e) {
      addToast(e.message, 'error')
    }
  }

  // A JSON export becomes a new chat. The file keeps its model unless this
  // server does not have it: then the chat gets the first chat model here.
  const importChatFile = async (e) => {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    try {
      const exported = chatFromExport(await file.text())
      if (!exported) throw new Error(t('toasts.notAChat'))
      const known = chatModels.some(m => m.id === exported.model)
      const conv = await chatsApi.importChat(exported, known ? '' : (chatModels[0]?.id || ''))
      addToast(t('toasts.imported', { title: conv.title || tc('list.untitled') }), 'success', 2500)
      navigate(`/app/chats/${encodeURIComponent(conv.id)}`)
    } catch (err) {
      addToast(t('toasts.importFailed', { message: err.message }), 'error')
    }
  }

  const promptDeleteAll = () => setConfirmDialog({
    title: t('deleteAllDialog.title'),
    message: t('deleteAllDialog.message'),
    confirmLabel: t('deleteAllDialog.confirm'),
    danger: true,
    onConfirm: async () => {
      setConfirmDialog(null)
      for (const c of visibleChats) {
        try { await chatsApi.delete(c.id) } catch (err) { addToast(err.message || tc('errors.delete'), 'error'); break }
      }
      refresh()
    },
  })

  return (
    <Page data-testid="all-chats-page">
      <PageHeader
        title={t('allChats.title')}
        supporting={t('allChats.supporting')}
        actions={(
          <Button size="sm" icon="import" onClick={() => importInputRef.current?.click()} data-testid="all-chats-import">
            {t('menu.importChat')}
          </Button>
        )}
      />
      {state.status === 'loading' && <SkeletonRows count={6} />}
      {state.status === 'error' && (
        <LoadError title={t('allChats.loadFailed')} error={state.error} onRetry={() => load()} nothingChanged={false} />
      )}
      {state.status === 'ready' && visibleChats.length === 0 && !deletes.pending && (
        <EmptyState
          testId="all-chats-empty"
          title={t('allChats.emptyTitle')}
          body={t('allChats.emptyBody')}
          actions={<Button variant="primary" icon="chat" onClick={() => navigate('/app')}>{t('allChats.start')}</Button>}
        />
      )}
      {state.status === 'ready' && (visibleChats.length > 0 || deletes.pending) && (
        <div className="cx-menu cx-menu--inline" data-testid="all-chats-list">
          <ChatsList
            chats={visibleChats}
            idPrefix="all-chats"
            onSelect={(id) => navigate(`/app/chats/${encodeURIComponent(id)}`)}
            onNew={() => navigate('/app')}
            onDelete={(c) => deletes.request({ id: c.id, name: c.name || tc('list.untitled') })}
            onDeleteAll={promptDeleteAll}
            onRename={rename}
            onExport={(c) => downloadFrom(chatsApi.exportUrl(c.id, 'markdown'))}
            onCopyChat={copyChat}
            onSearch={(text) => chatsApi.search(text, 50)}
          />
        </div>
      )}
      <input ref={importInputRef} type="file" accept=".json,application/json" hidden onChange={importChatFile} data-testid="chat-import-input" />
      {deletes.pending && (
        <HomeUndoToast
          key={deletes.pending.id}
          message={t('menu.deleted', { title: deletes.pending.name })}
          onUndo={deletes.undo}
          onExpire={deletes.commit}
          duration={CHAT_UNDO_MS}
          undoLabel={t('menu.undo')}
          dismissLabel={t('menu.dismiss')}
          testId="chat-undo-toast"
        />
      )}
      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title}
        message={confirmDialog?.message}
        confirmLabel={confirmDialog?.confirmLabel}
        danger={confirmDialog?.danger}
        onConfirm={confirmDialog?.onConfirm}
        onCancel={() => setConfirmDialog(null)}
      />
    </Page>
  )
}
