import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../context/AuthContext'
import { useWellKnown } from '../../hooks/useWellKnown'
import { filesApi } from '../../utils/api'
import { greetingKey } from '../../utils/greeting'
import { identityOf, uiStrings, fillTemplate, avatarPath } from '../../utils/look'
import './wellKnown.css'

// useIdentityVars are the values of the placeholders of the identity texts,
// in the language of the page: {user.name} from user.json or the account,
// {time_of_day} as the greeting of the hour ("Good morning"), {date} as the
// short date of Home.
export function useIdentityVars(userName) {
  const { t, i18n } = useTranslation('home')
  const { user } = useAuth()
  return useMemo(() => {
    let date = ''
    try { date = new Intl.DateTimeFormat(i18n.language, { weekday: 'short', day: 'numeric', month: 'short' }).format(new Date()) } catch { date = '' }
    return {
      'user.name': (typeof userName === 'string' && userName.trim()) || user?.name || '',
      time_of_day: t(`greeting.${greetingKey()}`),
      date,
    }
  }, [t, i18n.language, userName, user?.name])
}

// useIdentity reads how the assistant shows itself: its name, its picture, the
// greeting, the empty chat and the Home headline (persona.json, home.json and
// the text overrides of ui.json). Every reader shares one copy of each file,
// and the change feed brings in a change the assistant or a settings tab made.
// A broken file gives its last good version; a text with a broken placeholder
// is left out, so the default shows.
export function useIdentity() {
  const { hasFeature } = useAuth()
  const on = !hasFeature || hasFeature('chats')
  const persona = useWellKnown('persona', { enabled: on }).doc?.data
  const userData = useWellKnown('user', { enabled: on }).doc?.data
  const home = useWellKnown('home', { enabled: on }).doc?.data
  const ui = useWellKnown('ui', { enabled: on }).doc?.data
  const vars = useIdentityVars(userData?.name)
  return useMemo(() => identityOf({ persona, home, ui }, vars), [persona, home, ui, vars])
}

// useUiString answers the text of a key of ui.json strings, filled with vars,
// or fallback when the user has not changed it. Only the keys of the
// allow-list are read; the server refuses the others.
export function useUiString() {
  const { hasFeature } = useAuth()
  const on = !hasFeature || hasFeature('chats')
  const ui = useWellKnown('ui', { enabled: on }).doc?.data
  const strings = useMemo(() => uiStrings(ui), [ui])
  return useCallback((key, fallback, vars) => (strings[key] ? fillTemplate(strings[key], vars || {}) : fallback), [strings])
}

// AssistantAvatar is the picture of persona.json, read from the user's files
// with their own rights, or the first letter of the name when there is no
// picture or it does not load. It draws nothing without either.
export function AssistantAvatar({ avatar, name, size = 'sm', testId }) {
  const path = avatarPath(avatar)
  const [failed, setFailed] = useState('')
  const initial = (name || '').trim().charAt(0).toUpperCase()
  if (!path && !initial) return null
  const showImg = path && failed !== path
  return (
    <span className={`wk-avatar wk-avatar--${size}`} aria-hidden="true" data-testid={testId}>
      {showImg
        ? <img src={filesApi.contentUrl(`/${path}`, true)} alt="" onError={() => setFailed(path)} />
        : <span>{initial || '?'}</span>}
    </span>
  )
}
