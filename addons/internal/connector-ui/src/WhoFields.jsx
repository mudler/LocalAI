/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// "Who can talk to the bot": one choice, then the people and the channels the
// owner chose. People are listed with a badge (Verified or Listed) and can be
// verified or removed; channels have a level (answer only, or let everyone
// there ask for actions). One box adds a name or an ID and puts it where the
// kind says it belongs. The saved fields do not change: accept_any, allow,
// allow_trust and paired.
import { useState } from 'react'
import Icon from '../../host-compat/src/components/Icon'
import { Check, Field, Notice, RadioCards, Select } from './controls'
import { placeFor, trustAfter, trustOf, withPlace } from './connector'
import { nameOf } from './people'
import { usePeople } from './Person'
import { PairingNotices, time, usePairing } from './pairing'

// A row's name and, small, its ID.
function Who({ id, name }) {
  return (
    <span className="cn-who__who">
      <span className="cn-who__name">{name || id}</span>
      {name && <span className="cn-who__id cn-mono">{id}</span>}
    </span>
  )
}

function LevelSelect({ entry, level, onChange, t, testId = 'connector-trust-level' }) {
  return (
    <Select data-testid={testId} aria-label={t('who.levelFor', { channel: entry })} value={level} onChange={(e) => onChange(e.target.value)}>
      <option value="answer">{t('trust.answer')}</option>
      <option value="act">{t('trust.act')}</option>
    </Select>
  )
}

// Whether the bot may post in a listening channel: the choice of its place as
// text with a way to it, or a button that makes the channel a place with the
// safe default (a verified person must ask). Listening and posting stay two
// settings; this only shows them side by side.
function PostStatus({ form, id, name, s, onAdd, onShow }) {
  const place = placeFor(form, id)
  if (!place) {
    return (
      <span className="cn-who__post" data-testid="connector-post-status" data-post="none">
        <span className="cn-muted">{s('who.post.none')}</span>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="connector-post-add" aria-label={s('who.post.addFor', { name })} onClick={onAdd}>{s('who.post.add')}</button>
      </span>
    )
  }
  return (
    <span className="cn-who__post" data-testid="connector-post-status" data-post={place.approved ? 'any' : 'verified'}>
      <span className="cn-muted">{place.approved ? s('who.post.any') : s('who.post.verified')}</span>
      <button type="button" className="cn-link cn-link--text" data-testid="connector-post-show" aria-label={s('who.post.showFor', { name })} onClick={onShow}>{s('who.post.show')}</button>
    </span>
  )
}

/**
 * @param {object} props
 * @param {object} props.form
 * @param {Function} props.setForm
 * @param {Function} props.t            the words of the add-on, then the shared ones
 * @param {Function} props.s            the shared words
 * @param {boolean} props.confirmed     the "anyone" warning is confirmed
 * @param {Function} props.setConfirmed
 * @param {Function} props.isChannel    whether an entry names a channel or group (a kind knows its IDs)
 * @param {boolean} [props.mixed]       a kind that cannot tell a person from a channel by its ID keeps one list and
 *                                      shows the level on every entry
 * @param {Function} [props.onPaired]   called with the pairings of the binding when they change
 * @param {Array} props.recent          people of the sender log who are not listed: { id, name }
 * @param {Map} props.chatNames         chat ID -> name
 * @param {string} [props.hint]         the placeholder of the add box
 * @param {string} [props.help]         a line under the add box
 */
export default function WhoFields({ form, setForm, t, s, confirmed, setConfirmed, isChannel, mixed = false, onPaired, recent, chatNames, hint, help }) {
  const pairing = usePairing(form, onPaired)
  const people = usePeople()
  const [text, setText] = useState('')
  const anyone = Boolean(form.accept_any)
  const allow = anyone ? [] : form.allow || []
  const verified = new Map((pairing.paired || []).map((p) => [p.sender_id, p]))
  const channels = mixed ? [] : allow.filter((x) => isChannel(x))
  const listed = mixed ? allow : allow.filter((x) => !isChannel(x))
  // A verified person who is not on the list still shows, so the owner can see and remove it.
  const onlyVerified = [...verified.keys()].filter((x) => !allow.includes(x))
  const rows = [...listed, ...onlyVerified]

  const setAllow = (next) => setForm({ ...form, allow: next, allow_trust: trustAfter(form, next, isChannel) })
  const add = (raw) => {
    const v = String(raw).trim()
    if (v && !allow.includes(v)) setAllow([...allow, v])
    setText('')
  }
  const remove = (id) => {
    setAllow(allow.filter((x) => x !== id))
    if (verified.has(id)) pairing.remove(id)
  }
  const canPost = (form.caps || []).includes('talkback.channels')
  // Jumps to the row of the place of a channel and puts the focus on its choice.
  const showPlace = (id) => {
    const row = document.getElementById(`cn-place-${id}`)
    row?.scrollIntoView?.({ block: 'center' })
    row?.querySelector('select')?.focus()
  }
  const setLevel = (entry, level) => setForm({ ...form, allow_trust: { ...(form.allow_trust || {}), [entry]: level } })
  const verifyDisabled = pairing.busy || pairing.full || !pairing.saved
  const verifyHint = !pairing.saved ? s('pair.needSave') : pairing.full ? s('pair.full') : s('pair.startHelp')

  const personRow = (id) => {
    const p = verified.get(id)
    const entry = mixed && !onlyVerified.includes(id)
    return (
      <li key={id} className={`cn-who__row${entry ? ' cn-who__row--mixed' : ''}`} data-testid="connector-person-row" data-id={id} data-verified={p ? 'true' : 'false'}>
        <Who id={id} name={nameOf(people, id) || chatNames.get(id) || ''} />
        <span className="cn-who__badges">
          <span className={`cn-badge${p ? ' cn-badge--ok' : ''}`} data-testid="connector-person-badge">{p ? s('who.verified') : s('who.listed')}</span>
          {p && <span className="cn-muted cn-who__ends">{s('pair.expires', { time: time(p.expires_at) })}</span>}
        </span>
        {entry && <LevelSelect entry={id} level={trustOf(form, id)} onChange={(v) => setLevel(id, v)} t={s} />}
        <span className="cn-who__actions">
          {!p && (
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="connector-verify" disabled={verifyDisabled}
              aria-label={s('who.verifyPerson', { name: nameOf(people, id) || id })} onClick={pairing.start}>{s('who.verify')}</button>
          )}
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm"
            aria-label={s('who.removePerson', { name: nameOf(people, id) || id })} onClick={() => remove(id)}>{s('who.remove')}</button>
        </span>
      </li>
    )
  }

  return (
    <fieldset className="cn-group" data-testid="connector-who" tabIndex={-1}>
      <legend>{s('who.title')}</legend>
      <RadioCards name="who" legend={s('who.title')} value={anyone} onChange={(any) => { setForm({ ...form, accept_any: any }); if (!any) setConfirmed(false) }}
        options={[{ value: false, label: s('who.onlyChosen') }, { value: true, label: s('who.anyone') }]} />
      {anyone && (
        <Notice tone="warn" role="alert" testId="anyone-warning">
          <p className="cn-notice__text">{t('anyoneWarning')}</p>
          <Check checked={confirmed} onChange={setConfirmed}>{t('anyoneConfirm')}</Check>
        </Notice>
      )}

      <div className="cn-sub" data-testid="connector-pairing">
        <h3 className="cn-sub__title">{mixed ? s('who.peopleAndChannels') : s('who.people')}</h3>
        <PairingNotices p={pairing} s={s} />
        {rows.length === 0
          ? <p className="cn-muted">{anyone ? s('pair.none') : s('who.peopleNone')}</p>
          : <ul className="cn-who__list" aria-label={mixed ? s('who.peopleAndChannels') : s('who.people')} data-testid="connector-who-people">{rows.map(personRow)}</ul>}
        <div className="cn-who__verify">
          <button type="button" className="dk-btn dk-btn--secondary" data-testid="connector-pair-start" disabled={verifyDisabled} onClick={pairing.start}>{s('pair.start')}</button>
          <p className="dk-hint">{verifyHint}</p>
        </div>
      </div>

      {!anyone && channels.length > 0 && (
        <div className="cn-sub" data-testid="connector-who-channels">
          <h3 className="cn-sub__title">{s('who.channels')}</h3>
          <ul className="cn-who__list" aria-label={s('who.channels')}>
            {channels.map((id) => (
              <li key={id} className="cn-who__row" data-testid="connector-trust-row" data-channel={id} data-level={trustOf(form, id)}>
                <Who id={id} name={chatNames.get(id) || ''} />
                <LevelSelect entry={id} level={trustOf(form, id)} onChange={(v) => setLevel(id, v)} t={s} />
                <span className="cn-who__actions">
                  <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm"
                    aria-label={s('who.removeChannel', { name: chatNames.get(id) || id })} onClick={() => setAllow(allow.filter((x) => x !== id))}>{s('who.remove')}</button>
                </span>
                {canPost && <PostStatus form={form} id={id} name={chatNames.get(id) || id} s={s}
                  onAdd={() => setForm(withPlace(form, id, chatNames.get(id) || ''))} onShow={() => showPlace(id)} />}
              </li>
            ))}
          </ul>
          <p className="dk-hint" data-testid="connector-who-channels-note">{s('who.channelsNote')}</p>
        </div>
      )}

      {!anyone && (
        <div data-testid="connector-allow">
          <Field label={s('who.add')} hint={help}>
            {(p) => (
              <div className="cn-add">
                <input {...p} className="dk-input" value={text} placeholder={hint} onChange={(e) => setText(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add(text) } }} />
                <button type="button" className="dk-btn dk-btn--secondary" onClick={() => add(text)} disabled={!text.trim()}>{s('add')}</button>
              </div>
            )}
          </Field>
          {recent.length > 0 && (
            <div className="cn-who__recent" data-testid="connector-recent">
              <p className="dk-hint">{s('who.recent')}</p>
              <ul className="cn-chips" aria-label={s('who.recent')}>
                {recent.map((r) => (
                  <li key={r.id}>
                    <button type="button" className="dk-chip dk-chip--sm cn-who__chip" data-testid="connector-recent-chip" data-id={r.id}
                      aria-label={s('who.addPerson', { name: r.name || r.id })} onClick={() => add(r.id)}>
                      <Icon name="plus" /><span>{r.name ? `${r.name} · ${r.id}` : r.id}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </fieldset>
  )
}
