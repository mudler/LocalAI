/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The channels where the bot listens, and what the people who write in each one
// may do. Every channel of the allowlist has two choices: "Answer only" (the bot
// answers there, nobody in it is trusted one by one) or "Let everyone here ask
// for actions" (everyone who posts there may use the actions that are not
// sensitive, as a listed person does). This is not the list of channels the bot
// posts to: that is "Where it may post" under the permissions.
import { Notice, RadioCards } from './controls'
import { channelEntries, trustOf } from './connector'

export default function ChannelTrust({ form, setForm, t, isChannel }) {
  const channels = form.accept_any ? [] : channelEntries(form, isChannel)
  if (channels.length === 0) return null
  const set = (entry, level) => setForm({ ...form, allow_trust: { ...(form.allow_trust || {}), [entry]: level } })
  const noCaps = (form.caps || []).length === 0
  return (
    <div className="cn-sub" data-testid="connector-trust">
      <h3 className="cn-sub__title">{t('trust.title')}</h3>
      <p className="dk-hint">{t('trust.help')}</p>
      <ul className="cn-trust">
        {channels.map((entry) => {
          const level = trustOf(form, entry)
          return (
            <li key={entry} className="cn-trust__row" data-testid="connector-trust-row" data-channel={entry} data-level={level}>
              <span className="cn-trust__id" title={t('trust.nameHint')}>{entry}</span>
              <RadioCards name={`trust-${entry}`} legend={t('trust.switch', { channel: entry })} value={level} onChange={(v) => set(entry, v)}
                options={[{ value: 'answer', label: t('trust.answer') }, { value: 'act', label: t('trust.act') }]} />
              <p className="cn-trust__effect" data-testid="connector-trust-effect">{level === 'act' ? t('trust.consequence.act') : t('trust.consequence.answer')}</p>
            </li>
          )
        })}
      </ul>
      {noCaps && channels.some((x) => trustOf(form, x) === 'act') && (
        <Notice tone="warn" role="note" testId="connector-trust-nocaps"><p className="cn-notice__text">{t('trust.emptyCeiling')}</p></Notice>
      )}
    </div>
  )
}
