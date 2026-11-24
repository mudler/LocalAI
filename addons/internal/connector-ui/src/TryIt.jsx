/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// "Try it": pick who writes, where, and what they ask for, and read what the
// host would do, in one sentence. The answer is the access preview of the form
// as it is now (the page asks the host once for the whole form); nothing is
// sent to the platform and nothing is saved.
import { useState } from 'react'
import { Field, Select } from './controls'
import { FixButton } from './accessHooks'
import { sentenceCase } from './access'
import { placeFor, tryAnswer, tryChoices } from './check'
import { usePeople } from './Person'

const label = (c, s) => (c.kind === 'person' ? c.name || c.id : c.kind === 'channel' ? s('try.someoneIn', { channel: c.name || c.id }) : c.kind === 'anyone' ? s('try.anyone') : s('try.role', { role: c.name || c.id }))
const placeLabel = (p, s) => (p.kind === 'dm' ? s('try.dm') : p.name || p.id)

// The sentence that says who the sender is, for the kind of sender the host answered for.
function whoSentence(who, place, s) {
  const name = who.name || who.id
  const where = place.kind === 'dm' ? s('try.where.dm') : s('try.where.channel', { channel: place.name || place.id })
  switch (who.subject) {
    case 'paired': return s('try.is.paired', { who: name, where })
    case 'listed': return s('try.is.listed', { who: name, where })
    case 'channel_acts': return s('try.is.channelActs', { channel: who.name || who.id })
    case 'anyone': return who.kind === 'channel' ? s('try.is.channelAnswers', { channel: who.name || who.id }) : s('try.is.anyone')
    default: return s('try.is.role', { role: who.name || who.id })
  }
}

export default function TryIt({ form, capabilities, preview, kindInfo, s, ctx, paired, isChannel, mixed, chatNames }) {
  const people = usePeople()
  const { who, places } = tryChoices({ form, people, chatNames, paired, isChannel, mixed, roles: kindInfo?.roles })
  const [whoValue, setWho] = useState('')
  const [placeValue, setPlace] = useState('dm')
  const [cap, setCap] = useState('')
  const pickedWho = who.find((x) => x.value === whoValue) || who[0] || null
  const forced = placeFor(pickedWho)
  const place = places.find((p) => p.value === (forced || placeValue)) || places[0]
  const capName = capabilities.some((c) => c.name === cap) ? cap : capabilities[0]?.name || ''
  const view = preview.status === 'ready' || (preview.status === 'loading' && preview.view) ? preview.view : null
  const answer = tryAnswer({ view, who: pickedWho, capability: capName })
  const title = (c) => (c.name === 'tools.write' ? s('matrix.row.tools') : sentenceCase(c.title))
  return (
    <div className="cn-sub cn-try" data-testid="connector-try">
      <h3 className="cn-sub__title">{s('try.title')}</h3>
      <p className="dk-hint">{s('try.help')}</p>
      {who.every((x) => x.kind === 'role') ? <p className="cn-muted" data-testid="connector-try-none">{s('try.noone')}</p> : (<>
        <div className="cn-try__selects">
          <Field label={s('try.whoLabel')}>
            {(p) => (
              <Select {...p} data-testid="connector-try-who" value={pickedWho.value} onChange={(e) => setWho(e.target.value)}>
                {who.map((x) => <option key={x.value} value={x.value}>{label(x, s)}</option>)}
              </Select>
            )}
          </Field>
          <Field label={s('try.placeLabel')}>
            {(p) => (
              <Select {...p} data-testid="connector-try-place" value={place.value} disabled={Boolean(forced)} onChange={(e) => setPlace(e.target.value)}>
                {places.map((x) => <option key={x.value} value={x.value}>{placeLabel(x, s)}</option>)}
              </Select>
            )}
          </Field>
          <Field label={s('try.actionLabel')}>
            {(p) => (
              <Select {...p} data-testid="connector-try-action" value={capName} onChange={(e) => setCap(e.target.value)}>
                {capabilities.map((c) => <option key={c.name} value={c.name}>{title(c)}</option>)}
              </Select>
            )}
          </Field>
        </div>
        <div className="cn-try__answer" role="status" aria-live="polite" data-testid="connector-try-answer" data-verdict={answer?.verdict || 'wait'}>
          {preview.status === 'error' ? <p>{s('try.error')}</p>
            : !answer ? <p className="cn-muted">{s('try.loading')}</p>
            : answer.verdict === 'unknown' ? <p>{s('try.unknown')}</p> : (<>
              <p>
                <strong className={`cn-try__verdict cn-try__verdict--${answer.verdict}`}>{s(`try.verdict.${answer.verdict}`)}</strong>{' '}
                <span>{whoSentence(pickedWho, place, s)}</span>{' '}
                <span>{answer.message}</span>
              </p>
              {answer.then.length > 0 && <p className="cn-muted" data-testid="connector-try-then">{s('can.then', { list: answer.then.map((x) => s(`can.then.${x}`)).join(', ') })}</p>}
              {answer.fix && <div className="cn-can__fix"><FixButton fix={answer.fix} ctx={ctx} s={s} /></div>}
            </>)}
        </div>
      </>)}
      <p className="dk-hint" data-testid="connector-try-note">{s('try.note')}</p>
    </div>
  )
}
