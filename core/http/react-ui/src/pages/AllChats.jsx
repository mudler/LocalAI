/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatNotificationsApi, chatsApi } from '../utils/api'
import { chatFromExport, chatMarkdown, downloadFrom, menuChats } from '../utils/chatThread'
import { copyToClipboard } from '../utils/clipboard'
import { CAP_CHAT } from '../utils/capabilities'
import { useModels } from '../hooks/useModels'
import { CHAT_UNDO_MS, useChatDelete } from '../hooks/useChatDelete'
import { announceChats } from '../hooks/useRecentChats'
import { useOriginContext } from '../hooks/useOriginContext'
import { attention, needsYouIds, readTab, writeTab } from '../utils/originChats'
import AutomatedChats from '../components/AutomatedChats'
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
//
// Two tabs. "My chats" lists the chats a person started. "From add-ons and
// automations" lists the others (a connector, a trigger, a job, a page
// started them), grouped by what started them, so a bot's conversations do not
// sit among the person's own. The browser remembers the tab.
export default function AllChats() {
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('chat')
  const { t: tc } = useTranslation('chats')
  const { models: chatModels } = useModels(CAP_CHAT)
  const [state, setState] = useState({ status: 'loading', chats: [], needsYou: new Set(), error: null })
  const [tab, setTabState] = useState(() => readTab())
  const setTab = (id) => { setTabState(id); writeTab(id) }
  const [confirmDialog, setConfirmDialog] = useState(null)
  const importInputRef = useRef(null)

  const load = useCallback(async (keep = false) => {
    if (!keep) setState(s => ({ ...s, status: 'loading', error: null }))
    try {
      // Each side has its own limit, so a burst of chats from a connector cannot
      // push a person's chats off the page. The inbox says which of the others
      // wait for the person; a failed read of it or of the others is not an error.
      const [mine, others, inbox] = await Promise.all([
        chatsApi.list(200, 'chat', 'person'),
        chatsApi.list(200, 'chat', 'automated').catch(() => null),
        chatNotificationsApi.list(true, 200).catch(() => null),
      ])
      const seen = new Set()
      const chats = [...(mine.conversations || []), ...(others?.conversations || [])]
        .filter(c => c.kind !== 'subagent' && !seen.has(c.id) && seen.add(c.id))
      setState({ status: 'ready', chats, needsYou: needsYouIds(inbox?.notifications), error: null })
    } catch (error) {
      // A failed refresh keeps the list on screen; a failed first load says so.
      setState(s => (keep && s.status === 'ready' ? s : { status: 'error', chats: [], needsYou: new Set(), error }))
    }
  }, [])
  useEffect(() => { load() }, [load])
  const refresh = useCallback(() => load(true), [load])

  const deletes = useChatDelete({
    remove: (id) => chatsApi.delete(id).finally(refresh),
    onFailed: (e) => addToast(e.message || tc('errors.delete'), 'error'),
  })

  const allVisible = useMemo(
    () => menuChats(state.chats).filter(c => c.id !== deletes.pending?.id),
    [state.chats, deletes.pending],
  )
  const visibleChats = useMemo(() => allVisible.filter(c => !c.origin), [allVisible])
  const automatedChats = useMemo(() => allVisible.filter(c => c.origin), [allVisible])
  const originCtx = useOriginContext(automatedChats.length > 0)
  const { waiting } = attention(automatedChats, state.needsYou)
  // The tabs appear once there is a second list to show.
  const showTabs = automatedChats.length > 0 || tab === 'automated'
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
      {state.status === 'ready' && showTabs && (
        <div className="dk-tabs ac-tabs" role="tablist" aria-label={t('allChats.tabsLabel')} data-testid="all-chats-tabs">
          {['mine', 'automated'].map(id => (
            <button
              key={id}
              type="button"
              role="tab"
              id={`all-chats-tab-${id}`}
              aria-controls={`all-chats-panel-${id}`}
              aria-selected={tab === id}
              tabIndex={tab === id ? 0 : -1}
              className="dk-tab"
              data-testid={`all-chats-tab-${id}`}
              onClick={() => setTab(id)}
              onKeyDown={(e) => {
                if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return
                e.preventDefault()
                const next = id === 'mine' ? 'automated' : 'mine'
                setTab(next)
                document.getElementById(`all-chats-tab-${next}`)?.focus()
              }}
            >
              {t(`allChats.tab.${id}`)}
              <span className="ac-tab__count" data-testid={`all-chats-count-${id}`}>{id === 'mine' ? visibleChats.length : automatedChats.length}</span>
              {id === 'automated' && waiting > 0 && (
                <span className="ac-tab__wait" data-testid="all-chats-waiting" title={t('allChats.automated.waitingCount', { count: waiting })} aria-label={t('allChats.automated.waitingCount', { count: waiting })}>{waiting}</span>
              )}
            </button>
          ))}
        </div>
      )}
      {state.status === 'loading' && <SkeletonRows count={6} />}
      {state.status === 'error' && (
        <LoadError title={t('allChats.loadFailed')} error={state.error} onRetry={() => load()} nothingChanged={false} />
      )}
      {state.status === 'ready' && tab === 'automated' && (
        <div role={showTabs ? 'tabpanel' : undefined} id="all-chats-panel-automated" aria-labelledby="all-chats-tab-automated">
          <AutomatedChats
            chats={automatedChats}
            ctx={originCtx}
            needsYou={state.needsYou}
            onOpen={(id) => navigate(`/app/chats/${encodeURIComponent(id)}`)}
            onDelete={(c) => deletes.request({ id: c.id, name: c.name || tc('list.untitled') })}
          />
        </div>
      )}
      {state.status === 'ready' && tab === 'mine' && visibleChats.length === 0 && !deletes.pending && (
        <EmptyState
          testId="all-chats-empty"
          title={t('allChats.emptyTitle')}
          body={t('allChats.emptyBody')}
          actions={<Button variant="primary" icon="chat" onClick={() => navigate('/app')}>{t('allChats.start')}</Button>}
        />
      )}
      {state.status === 'ready' && tab === 'mine' && (visibleChats.length > 0 || deletes.pending) && (
        <div className="cx-menu cx-menu--inline" role={showTabs ? 'tabpanel' : undefined} id="all-chats-panel-mine" aria-labelledby={showTabs ? 'all-chats-tab-mine' : undefined} data-testid="all-chats-list">
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
