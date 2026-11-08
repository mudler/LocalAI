/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import SideSheet from '../SideSheet'
import { Button, Segmented, StatusChip } from '../ui'
import { MAX_CONTACTS, contactError, emptyContact } from '../../utils/coordinator'

// A reply into the coordinator's chat is routed by sender or by chat; the
// other conversation modes cannot carry it (see the contacts help).
const REPLY_MODES = ['sender', 'chat']

// What a contact points at, in one line.
function targetOf(t, ct, bindings) {
  if (ct.kind === 'user') return t('coordinator.contacts.userTarget', { user: ct.user })
  const b = bindings.find(x => x.id === ct.binding)
  return t('coordinator.contacts.externalTarget', { binding: b ? b.name : ct.binding, chat: ct.chat })
}

// The contacts of a coordinator: the only people it can write to. The list is
// edited in a side sheet and saved with the coordinator.
//   value      the contacts of the form
//   onChange   called with the new list
//   bindings   the person's messaging connections (chatConnectorsApi.list), or null while unread
//   me         the person's own user id, when known
//   error      { index, code } from the last save attempt
export default function CoordinatorContacts({ value, onChange, bindings, bindingsFailed, me, error }) {
  const { t } = useTranslation('agents')
  const [editing, setEditing] = useState(null) // { index, draft } (index -1 for a new one)
  const list = bindings || []
  const usable = list.filter(b => !b.credentials_only)

  const save = (draft, index) => {
    const next = index < 0 ? [...value, draft] : value.map((c, i) => (i === index ? draft : c))
    onChange(next)
    setEditing(null)
  }
  const remove = (index) => onChange(value.filter((_, i) => i !== index))

  return (
    <div className="crd-contacts" data-testid="coordinator-contacts">
      {value.length === 0 ? (
        <p className="ag-note" data-testid="coordinator-contacts-empty">{t('coordinator.contacts.none')}</p>
      ) : (
        <ul className="crd-contact-list">
          {value.map((ct, i) => (
            <li key={`${ct.id}-${i}`} className="crd-contact" data-testid="coordinator-contact" data-invalid={error?.index === i || undefined}>
              <div className="crd-contact__main">
                <span className="crd-contact__name">{ct.name || ct.id}</span>
                <code className="crd-contact__id">{ct.id}</code>
                <span className="crd-contact__target">{targetOf(t, ct, list)}</span>
                {error?.index === i && <span className="dk-field-error" role="alert">{t(`coordinator.contacts.errors.${error.code}`)}</span>}
              </div>
              <StatusChip tone="idle" dot>{t(`coordinator.contacts.kind.${ct.kind}`)}</StatusChip>
              <div className="crd-contact__acts">
                <Button size="sm" variant="ghost" onClick={() => setEditing({ index: i, draft: ct })} aria-label={t('coordinator.contacts.editFor', { name: ct.name || ct.id })}>{t('coordinator.contacts.edit')}</Button>
                <Button size="sm" variant="danger-ghost" onClick={() => remove(i)} aria-label={t('coordinator.contacts.removeFor', { name: ct.name || ct.id })}>{t('coordinator.contacts.remove')}</Button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {error?.index === -1 && <p className="dk-field-error" role="alert">{t(`coordinator.contacts.errors.${error.code}`)}</p>}
      <div>
        <Button size="sm" icon="plus" disabled={value.length >= MAX_CONTACTS} onClick={() => setEditing({ index: -1, draft: emptyContact('user') })} data-testid="coordinator-contact-add">{t('coordinator.contacts.add')}</Button>
      </div>
      {editing && (
        <ContactSheet
          draft={editing.draft} index={editing.index} taken={value.filter((_, i) => i !== editing.index).map(c => c.id)}
          bindings={usable} bindingsFailed={bindingsFailed} me={me}
          onSave={save} onClose={() => setEditing(null)}
        />
      )}
    </div>
  )
}

function Field({ id, label, help, error, optional, children }) {
  const { t } = useTranslation('agents')
  return (
    <div className="dk-field">
      <label className="dk-label" htmlFor={id}>{label}{optional && <span className="crd-optional"> {t('coordinator.optional')}</span>}</label>
      {children}
      {error
        ? <p className="dk-field-error" id={`${id}-msg`} role="alert">{error}</p>
        : help && <p className="dk-hint" id={`${id}-msg`}>{help}</p>}
    </div>
  )
}

// The first problem of a draft: the ID first (a bad one, then one another
// contact already has), then the other fields.
function problemOf(draft, taken) {
  const code = contactError(draft)
  if (code === 'id') return code
  return taken.includes(draft.id.trim()) ? 'idTwice' : code
}

// The editor of one contact. Save checks the same rules as the server and
// says what is wrong next to the field.
function ContactSheet({ draft: initial, index, taken, bindings, bindingsFailed, me, onSave, onClose }) {
  const { t } = useTranslation('agents')
  const [draft, setDraft] = useState(initial)
  const [tried, setTried] = useState(false)
  const set = (patch) => setDraft(d => ({ ...d, ...patch }))
  const code = tried ? problemOf(draft, taken) : ''
  const err = (field) => (code === field || (field === 'id' && code === 'idTwice') ? t(`coordinator.contacts.errors.${code}`) : undefined)
  const binding = bindings.find(b => b.id === draft.binding)
  const unusable = binding && !REPLY_MODES.includes(binding.conversation)
  const chats = (binding?.allow || []).filter(Boolean)

  const submit = (e) => {
    e.preventDefault()
    // The sheet is a portal inside the page's form in the React tree: without
    // this the page form would also see the submit and try to save.
    e.stopPropagation()
    setTried(true)
    const problem = problemOf(draft, taken)
    if (problem) {
      const field = problem === 'idTwice' ? 'id' : problem
      document.getElementById(`crd-c-${field}`)?.focus()
      return
    }
    onSave({ ...draft, id: draft.id.trim() }, index)
  }

  return (
    <SideSheet
      title={index < 0 ? t('coordinator.contacts.newTitle') : t('coordinator.contacts.editTitle')}
      description={t('coordinator.contacts.sheetDesc')}
      onClose={onClose}
      closeLabel={t('create.close')}
      labelId="coordinator-contact-title"
      testId="coordinator-contact-sheet"
      footer={(
        <>
          <Button variant="ghost" onClick={onClose}>{t('coordinator.cancel')}</Button>
          <Button type="submit" form="coordinator-contact-form" variant="primary">{t('coordinator.contacts.done')}</Button>
        </>
      )}
    >
      <form id="coordinator-contact-form" className="crd-sheet" onSubmit={submit} noValidate>
        <div className="dk-field">
          <span className="dk-label" id="crd-c-kind-l">{t('coordinator.contacts.kindLabel')}</span>
          <Segmented
            label={t('coordinator.contacts.kindLabel')}
            value={draft.kind}
            onChange={(kind) => set({ kind })}
            itemTestId="coordinator-contact-kind-"
            options={[{ id: 'user', label: t('coordinator.contacts.kind.user') }, { id: 'external', label: t('coordinator.contacts.kind.external') }]}
          />
          <p className="dk-hint">{draft.kind === 'user' ? t('coordinator.contacts.kindUserHelp') : t('coordinator.contacts.kindExternalHelp')}</p>
        </div>
        <Field id="crd-c-id" label={t('coordinator.contacts.id')} help={t('coordinator.contacts.idHelp')} error={err('id')}>
          <input id="crd-c-id" className="dk-input" value={draft.id} maxLength={40} autoComplete="off" placeholder="ops-team"
            onChange={(e) => set({ id: e.target.value })} aria-invalid={err('id') ? true : undefined} aria-describedby="crd-c-id-msg" />
        </Field>
        <Field id="crd-c-name" label={t('coordinator.contacts.name')} optional error={err('name')}>
          <input id="crd-c-name" className="dk-input" value={draft.name} autoComplete="off" onChange={(e) => set({ name: e.target.value })} />
        </Field>
        <Field id="crd-c-description" label={t('coordinator.contacts.description')} help={t('coordinator.contacts.descriptionHelp')} optional error={err('description')}>
          <input id="crd-c-description" className="dk-input" value={draft.description} autoComplete="off" onChange={(e) => set({ description: e.target.value })} />
        </Field>
        {draft.kind === 'user' ? (
          <Field id="crd-c-user" label={t('coordinator.contacts.user')} help={t('coordinator.contacts.userHelp')} error={err('user')}>
            <div className="crd-inline">
              <input id="crd-c-user" className="dk-input" value={draft.user} autoComplete="off" onChange={(e) => set({ user: e.target.value })} aria-invalid={err('user') ? true : undefined} aria-describedby="crd-c-user-msg" />
              {me && <Button size="sm" onClick={() => set({ user: me })} data-testid="coordinator-contact-me">{t('coordinator.contacts.me')}</Button>}
            </div>
          </Field>
        ) : (
          <>
            <Field id="crd-c-binding" label={t('coordinator.contacts.binding')} error={err('binding')}
              help={bindingsFailed ? t('coordinator.contacts.bindingsFailed') : bindings.length === 0 ? t('coordinator.contacts.noBindings') : unusable ? undefined : t('coordinator.contacts.bindingHelp')}>
              <span className="dk-select-wrap">
                <select id="crd-c-binding" className="dk-select" value={draft.binding} onChange={(e) => set({ binding: e.target.value })} aria-invalid={err('binding') ? true : undefined} aria-describedby="crd-c-binding-msg">
                  <option value="">{t('coordinator.contacts.pickBinding')}</option>
                  {bindings.map(b => <option key={b.id} value={b.id} disabled={b.enabled === false}>{b.name}{b.enabled === false ? ` (${t('coordinator.contacts.off')})` : ''}</option>)}
                </select>
              </span>
              {unusable && <p className="dk-field-error" role="alert" data-testid="coordinator-contact-mode">{t('coordinator.contacts.modeWarn', { mode: binding.conversation })}</p>}
            </Field>
            <Field id="crd-c-chat" label={t('coordinator.contacts.chat')} help={t('coordinator.contacts.chatHelp')} error={err('chat')}>
              <input id="crd-c-chat" className="dk-input" value={draft.chat} autoComplete="off" list="crd-c-chats" onChange={(e) => set({ chat: e.target.value })} aria-invalid={err('chat') ? true : undefined} aria-describedby="crd-c-chat-msg" />
              <datalist id="crd-c-chats">{chats.map(c => <option key={c} value={c} />)}</datalist>
            </Field>
            <Field id="crd-c-sender" label={t('coordinator.contacts.sender')} help={t('coordinator.contacts.senderHelp')} optional>
              <input id="crd-c-sender" className="dk-input" value={draft.sender} autoComplete="off" list="crd-c-chats" onChange={(e) => set({ sender: e.target.value })} />
            </Field>
          </>
        )}
      </form>
    </SideSheet>
  )
}
