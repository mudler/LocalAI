// The entry of the Contacts add-on. The page lists and searches the owner's
// address book, shows one contact, lets the owner add, change and delete
// contacts, and exports one vCard file. Every call goes to the add-on through
// the typed read of the host, as the signed-in person; the add-on keeps the
// book of that person and of nobody else.
import { locales } from './i18n/index.js'
import { argsOf, birthdayText, downloadVcard, emptyForm, emptyRow, formOf, initials, problem } from './lib/contacts.js'

const SEARCH_DELAY_MS = 250

export function createAddon(sdk) {
  const { React } = sdk
  const h = React.createElement
  for (const [language, bundle] of Object.entries(locales)) sdk.locale.define(language, bundle)
  const t = (key, vars) => {
    let s = sdk.locale.t(key, key)
    for (const [k, v] of Object.entries(vars || {})) s = s.split(`{{${k}}}`).join(String(v))
    return s
  }
  const call = (tool, args) => sdk.api.read({ arguments: { tool, ...(args || {}) } })
  const lang = () => { try { return sdk.locale.language() || 'en' } catch { return 'en' } }
  const fmtDate = (opts, y, m, d) => new Intl.DateTimeFormat(lang(), { timeZone: 'UTC', ...opts }).format(new Date(Date.UTC(y, m - 1, d)))
  const refused = (out) => (out && out.ok === false ? out.error || t('contacts.failed') : '')

  function Field({ label, id, children }) {
    return h('label', { className: 'ct-field', htmlFor: id }, h('span', { className: 'ct-field__label' }, label), children)
  }

  function Entries({ kind, rows, onChange, busy }) {
    const set = (i, k) => (e) => onChange(rows.map((r, j) => (j === i ? { ...r, [k]: e.target.value } : r)))
    const remove = (i) => onChange(rows.length > 1 ? rows.filter((_, j) => j !== i) : [emptyRow()])
    const plural = kind === 'phone' ? 'phones' : 'emails'
    return h('fieldset', { className: 'ct-entries', 'data-testid': `contacts-${plural}` },
      h('legend', null, t(`contacts.form.${plural}`)),
      rows.map((r, i) => h('div', { key: i, className: 'ct-entry' },
        h('input', { type: 'text', value: r.label, onChange: set(i, 'label'), maxLength: 40, placeholder: t('contacts.form.labelHint'), 'aria-label': t('contacts.form.label'), disabled: busy }),
        h('input', { type: kind === 'phone' ? 'tel' : 'email', value: r.value, onChange: set(i, 'value'), maxLength: kind === 'phone' ? 40 : 254, 'aria-label': t(`contacts.form.${kind}`), disabled: busy, 'data-testid': `contacts-${kind}-${i}` }),
        h('button', { type: 'button', className: 'btn btn-secondary', onClick: () => remove(i), disabled: busy }, t('contacts.form.remove')))),
      rows.length < 10 ? h('div', { className: 'ct-actions' },
        h('button', { type: 'button', className: 'btn btn-secondary', onClick: () => onChange([...rows, emptyRow()]), disabled: busy, 'data-testid': `contacts-add-${kind}` }, t(kind === 'phone' ? 'contacts.form.addPhone' : 'contacts.form.addEmail'))) : null)
  }

  function ContactForm({ initial, onSaved, onCancel }) {
    const { useState } = React
    const [f, setF] = useState(initial)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState('')
    const set = (k) => (e) => setF({ ...f, [k]: e.target.value })
    const save = async (e) => {
      e.preventDefault()
      const bad = problem(f)
      if (bad) { setError(t(bad)); return }
      setBusy(true)
      setError('')
      try {
        const out = await call(f.id ? 'ui-contact-update' : 'ui-contact-create', argsOf(f))
        const why = refused(out)
        if (why) setError(why)
        else onSaved(out.contact)
      } catch { setError(t('contacts.failed')) } finally { setBusy(false) }
    }
    const title = f.id ? t('contacts.form.editTitle') : t('contacts.form.newTitle')
    const text = (k, extra) => h('input', { id: `ct-${k}`, type: 'text', value: f[k], onChange: set(k), disabled: busy, 'data-testid': `contacts-${k}`, ...extra })
    return h('form', { className: 'ct-form', onSubmit: save, 'aria-label': title, 'data-testid': 'contacts-form' },
      h('h2', null, title),
      h(Field, { label: t('contacts.form.name'), id: 'ct-name' }, text('name', { maxLength: 200, required: true })),
      h(Field, { label: t('contacts.form.organisation'), id: 'ct-organisation' }, text('organisation', { maxLength: 200 })),
      h(Entries, { kind: 'phone', rows: f.phones, onChange: (phones) => setF({ ...f, phones }), busy }),
      h(Entries, { kind: 'email', rows: f.emails, onChange: (emails) => setF({ ...f, emails }), busy }),
      h(Field, { label: t('contacts.form.birthday'), id: 'ct-birthday' }, text('birthday', { maxLength: 10, placeholder: '1990-04-23' })),
      h('p', { className: 'ct-hint' }, t('contacts.form.birthdayHelp')),
      h(Field, { label: t('contacts.form.address'), id: 'ct-address' }, h('textarea', { id: 'ct-address', value: f.address, onChange: set('address'), rows: 2, maxLength: 500, disabled: busy, 'data-testid': 'contacts-address' })),
      h(Field, { label: t('contacts.form.notes'), id: 'ct-notes' }, h('textarea', { id: 'ct-notes', value: f.notes, onChange: set('notes'), rows: 3, maxLength: 4000, disabled: busy, 'data-testid': 'contacts-notes' })),
      error ? h('p', { className: 'ct-error', role: 'alert', 'data-testid': 'contacts-error' }, error) : null,
      h('div', { className: 'ct-actions' },
        h('button', { type: 'submit', className: 'btn btn-primary', disabled: busy, 'data-testid': 'contacts-save' }, t('contacts.form.save')),
        h('button', { type: 'button', className: 'btn btn-secondary', onClick: onCancel, disabled: busy, 'data-testid': 'contacts-cancel' }, t('contacts.form.cancel'))))
  }

  function Row({ c, current, onOpen }) {
    const sub = [c.organisation, c.phones[0] && c.phones[0].value, c.emails[0] && c.emails[0].value].filter(Boolean).join(' · ')
    return h('li', { 'data-testid': 'contacts-item' },
      h('button', { type: 'button', className: 'ct-row', 'aria-current': current ? 'true' : undefined, onClick: () => onOpen(c.id) },
        h('span', { className: 'ct-avatar', 'aria-hidden': 'true' }, initials(c.name)),
        h('span', { className: 'ct-row__text' }, h('span', { className: 'ct-row__name' }, c.name), sub ? h('span', { className: 'ct-row__sub' }, sub) : null)))
  }

  function Detail({ c, onEdit, onDeleted, onClose }) {
    const { useState } = React
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState('')
    const [confirm, setConfirm] = useState(false)
    const remove = async () => {
      setBusy(true)
      setError('')
      try {
        const why = refused(await call('ui-contact-delete', { id: c.id }))
        if (why) setError(why)
        else onDeleted()
      } catch { setError(t('contacts.failed')) } finally { setBusy(false) }
    }
    const list = (entries) => entries.map((e, i) => h('div', { key: i }, e.value, ' ', h('small', null, e.label || t('contacts.detail.noLabel'))))
    const row = (key, value) => (value ? [h('dt', { key: `${key}-t` }, t(`contacts.detail.${key}`)), h('dd', { key: `${key}-d`, className: key === 'notes' ? 'ct-notes' : undefined }, value)] : [])
    return h('section', { className: 'ct-detail', 'data-testid': 'contacts-detail' },
      h('h2', null, c.name),
      h('dl', null,
        row('organisation', c.organisation),
        row('phones', c.phones.length ? list(c.phones) : null),
        row('emails', c.emails.length ? list(c.emails) : null),
        row('birthday', c.birthday ? birthdayText(c.birthday, fmtDate) : ''),
        row('address', c.address),
        row('notes', c.notes)),
      error ? h('p', { className: 'ct-error', role: 'alert' }, error) : null,
      confirm
        ? h('div', { className: 'ct-actions', role: 'group', 'aria-label': t('contacts.confirm') },
          h('span', null, t('contacts.confirm')),
          h('button', { type: 'button', className: 'btn btn-danger', disabled: busy, 'data-testid': 'contacts-confirm', onClick: remove }, t('contacts.yes')),
          h('button', { type: 'button', className: 'btn btn-secondary', onClick: () => setConfirm(false) }, t('contacts.no')))
        : h('div', { className: 'ct-actions' },
          h('button', { type: 'button', className: 'btn btn-primary', onClick: () => onEdit(c), 'data-testid': 'contacts-edit' }, t('contacts.edit')),
          h('button', { type: 'button', className: 'btn btn-secondary', onClick: () => setConfirm(true), 'data-testid': 'contacts-delete' }, t('contacts.delete')),
          h('button', { type: 'button', className: 'btn btn-secondary', onClick: onClose }, t('contacts.close'))))
  }

  function List({ contacts, current, onOpen, searching }) {
    if (contacts.length === 0) return h('p', { className: 'ct-empty', 'data-testid': 'contacts-empty' }, t(searching ? 'contacts.noMatch' : 'contacts.empty'))
    return h('ul', { className: 'ct-list', 'data-testid': 'contacts-list' }, contacts.map((c) => h(Row, { key: c.id, c, current: c.id === current, onOpen })))
  }

  function Component({ page }) {
    if (page !== 'contacts') return null
    return h(Page, null)
  }

  function Page() {
    const { useState, useEffect, useCallback } = React
    const [query, setQuery] = useState('')
    const [applied, setApplied] = useState('')
    const [contacts, setContacts] = useState([])
    const [more, setMore] = useState(false)
    const [phase, setPhase] = useState('loading')
    const [form, setForm] = useState(null)
    const [picked, setPicked] = useState(null)
    const [note, setNote] = useState('')

    useEffect(() => {
      const id = setTimeout(() => setApplied(query.trim()), SEARCH_DELAY_MS)
      return () => clearTimeout(id)
    }, [query])

    const load = useCallback(async () => {
      try {
        const out = await call('ui-contacts-list', { query: applied })
        if (refused(out)) { setPhase('error'); return }
        setContacts(out.contacts || [])
        setMore(!!out.more)
        setPhase('ready')
      } catch { setPhase('error') }
    }, [applied])
    useEffect(() => { load() }, [load])

    const open = async (id) => {
      try {
        const out = await call('ui-contact-get', { id })
        if (refused(out)) { setPhase('error'); return }
        setForm(null)
        setPicked(out.contact)
      } catch { setPhase('error') }
    }
    const saved = (c) => { setForm(null); setPicked(c || null); load() }
    const deleted = () => { setPicked(null); load() }
    const exportVcf = async () => {
      setNote('')
      try {
        const out = await call('contacts-export')
        const why = refused(out)
        if (why) setNote(why)
        else downloadVcard(document, out.vcard, out.filename || 'contacts.vcf')
      } catch { setNote(t('contacts.failed')) }
    }

    if (phase === 'loading') return h('p', { 'aria-busy': 'true', 'data-testid': 'contacts-loading' }, t('contacts.loading'))
    if (phase === 'error') return h('div', { className: 'ct-page' }, h('p', { className: 'ct-error', role: 'alert', 'data-testid': 'contacts-unreadable' }, t('contacts.unreadable')))

    return h('section', { className: 'ct-page', 'data-testid': 'contacts-page' },
      h('header', { className: 'ct-bar' },
        h('h1', { className: 'page-title ct-bar__title' }, t('contacts.title')),
        h('div', { className: 'ct-bar__tools' },
          h('button', { type: 'button', className: 'btn btn-secondary', onClick: exportVcf, 'data-testid': 'contacts-export' }, t('contacts.export')),
          h('button', { type: 'button', className: 'btn btn-primary', onClick: () => { setPicked(null); setForm(emptyForm()) }, 'data-testid': 'contacts-new' }, t('contacts.new')))),
      note ? h('p', { role: 'status', className: 'ct-hint', 'data-testid': 'contacts-note' }, note) : null,
      h('input', { type: 'search', className: 'ct-search', value: query, onChange: (e) => setQuery(e.target.value), placeholder: t('contacts.searchHint'), 'aria-label': t('contacts.search'), 'data-testid': 'contacts-search' }),
      h('p', { className: 'ct-hint', role: 'status' }, t('contacts.count', { count: contacts.length })),
      more ? h('p', { className: 'ct-hint', role: 'status' }, t('contacts.truncated')) : null,
      h('div', { className: 'ct-body' },
        h(List, { contacts, current: picked && picked.id, onOpen: open, searching: !!applied }),
        form ? h(ContactForm, { key: form.id || 'new', initial: form, onSaved: saved, onCancel: () => setForm(null) })
          : picked ? h(Detail, { key: picked.id, c: picked, onEdit: (c) => { setPicked(null); setForm(formOf(c)) }, onDeleted: deleted, onClose: () => setPicked(null) })
            : h('p', { className: 'ct-hint' }, t('contacts.pickOne'))))
  }

  // The views are exposed so a test can draw each one with its own data.
  return { Component, components: { ContactsList: List, ContactsForm: ContactForm, ContactsDetail: Detail }, chatActions: [], dispose() {} }
}
