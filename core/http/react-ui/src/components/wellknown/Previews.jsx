/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button } from '../ui'
import { useWellKnown } from '../../hooks/useWellKnown'
import { shortcutList } from '../../utils/wellKnown'
import { identityOf, fillTemplate, uiStrings } from '../../utils/look'
import { AssistantAvatar, useIdentityVars } from './Identity'
import { defaultStarters } from './ShortcutsEditor'
import { useDefaultText } from './StringsField'

// IdentityPreview shows the identity the About tab is editing, before it is
// saved: Home, a new chat and the first line of a chat. persona and home are
// the values of the two forms; the text overrides of ui.json and the user's
// shortcuts come from the files.
export function IdentityPreview({ persona, home }) {
  const { t } = useTranslation('workspace')
  const { t: tc } = useTranslation('chat')
  const user = useWellKnown('user').doc?.data
  const ui = useWellKnown('ui').doc?.data
  const shortcuts = useWellKnown('shortcuts').doc
  const vars = useIdentityVars(user?.name)
  const id = useMemo(() => identityOf({ persona, home, ui }, vars), [persona, home, ui, vars])
  const chips = useMemo(() => shortcutList(shortcuts, defaultStarters(tc)).items.slice(0, 2), [shortcuts, tc])
  const defaults = useDefaultText()
  const name = id.name || t('identity.preview.assistant')

  return (
    <aside className="wk-preview" aria-label={t('identity.preview.title')} data-testid="wk-identity-preview">
      <p className="wk-preview__title"><Icon name="eye" /> {t('identity.preview.title')}</p>
      <section className="wk-preview__panel">
        <p className="wk-preview__label">{t('identity.preview.home')}</p>
        <div className="wk-preview__home">
          <AssistantAvatar avatar={id.avatar} name={id.name} size="md" />
          <strong className="wk-preview__headline" data-testid="wk-preview-headline">{id.headline || defaults['home.headline']}</strong>
        </div>
        <div className="wk-preview__box">{t('identity.messageName', { name })}</div>
      </section>
      <section className="wk-preview__panel wk-preview__panel--center">
        <p className="wk-preview__label">{t('identity.preview.newChat')}</p>
        <AssistantAvatar avatar={id.avatar} name={id.name} size="lg" />
        <strong className="wk-preview__headline" data-testid="wk-preview-empty-title">{id.emptyTitle || defaults['chat.empty.title']}</strong>
        {id.emptySubtitle && <span className="wk-preview__sub">{id.emptySubtitle}</span>}
        {id.showShortcuts && chips.length > 0 && (
          <span className="wk-preview__chips" data-testid="wk-preview-chips">
            {chips.map(c => <span key={c.label} className="wk-preview__chip">{c.label}</span>)}
          </span>
        )}
      </section>
      <section className="wk-preview__panel">
        <p className="wk-preview__label">{t('identity.preview.firstMessage')}</p>
        {id.greeting ? (
          <div className="wk-preview__greet">
            <AssistantAvatar avatar={id.avatar} name={id.name} size="sm" />
            <div><b>{name}</b><p data-testid="wk-preview-greeting">{id.greeting}</p></div>
          </div>
        ) : <p className="wk-preview__sub">{t('identity.preview.noGreeting')}</p>}
      </section>
    </aside>
  )
}

// LookPreview shows a short chat in the look the Appearance tab is editing.
// The tab puts the edited look on the page as it changes (applyAppearance),
// so this needs no props: it draws with the same tokens as the chat.
export function LookPreview() {
  const { t } = useTranslation('workspace')
  return (
    <aside className="wk-preview wk-lookprev" aria-label={t('look.preview.title')} data-testid="wk-look-preview">
      <p className="wk-preview__title"><Icon name="eye" /> {t('look.preview.title')}</p>
      <div className="wk-preview__panel wk-lookprev__chat">
        <div className="wk-lookprev__user"><span className="wk-lookprev__bubble wk-lookprev__text" data-testid="wk-lookprev-bubble">{t('look.preview.question')}</span></div>
        <p className="wk-lookprev__text">{t('look.preview.answer')}</p>
        <div className="code-block wk-lookprev__code" data-testid="wk-lookprev-code">
          <pre><code><span className="hljs-keyword">with</span> <span className="hljs-built_in">open</span>(<span className="hljs-string">&quot;notes.txt&quot;</span>) <span className="hljs-keyword">as</span> f:{'\n'}    text = f.read()  <span className="hljs-comment"># {t('look.preview.comment')}</span></code></pre>
        </div>
        <p className="wk-lookprev__text">{t('look.preview.after')}</p>
        <span><Button variant="primary" size="sm" tabIndex={-1}>{t('look.preview.button')}</Button></span>
        <div className="wk-preview__box wk-lookprev__text">{t('look.preview.box')}</div>
      </div>
    </aside>
  )
}

// StringsPreview shows the labels the Text tab is editing where they appear:
// the sidebar, the Home headline and the message box.
export function StringsPreview({ ui }) {
  const { t } = useTranslation('workspace')
  const defaults = useDefaultText()
  const userName = useWellKnown('user').doc?.data?.name
  const home = useWellKnown('home').doc?.data
  const vars = useIdentityVars(userName)
  const s = useMemo(() => uiStrings(ui), [ui])
  const text = (k, extra) => fillTemplate(s[k] || defaults[k], { ...vars, ...extra })
  // The headline of home.json, when there is one, wins over this text.
  const headline = identityOf({ home }, vars).headline || text('home.headline')
  return (
    <aside className="wk-preview" aria-label={t('strings.preview.title')} data-testid="wk-strings-preview">
      <p className="wk-preview__title"><Icon name="eye" /> {t('strings.preview.title')}</p>
      <section className="wk-preview__panel">
        <p className="wk-preview__label">{t('strings.preview.sidebar')}</p>
        <ul className="wk-preview__nav">
          <li><Icon name="home" /> <span data-testid="wk-preview-nav-home">{text('nav.home')}</span></li>
          <li className="wk-preview__navhead"><span>{text('nav.recentChats')}</span> <Icon name="plus" /> <span data-testid="wk-preview-nav-new">{text('nav.newChat')}</span></li>
          <li><Icon name="history" /> <span>{text('nav.allChats')}</span></li>
        </ul>
      </section>
      <section className="wk-preview__panel">
        <p className="wk-preview__label">{t('identity.preview.home')}</p>
        <strong className="wk-preview__headline" data-testid="wk-preview-strings-headline">{headline}</strong>
        <div className="wk-preview__box" data-testid="wk-preview-placeholder">{text('composer.placeholder', { model: 'qwen3-8b' })}</div>
      </section>
      <section className="wk-preview__panel">
        <p className="wk-preview__label">{t('identity.preview.newChat')}</p>
        <strong className="wk-preview__headline">{text('chat.empty.title')}</strong>
      </section>
    </aside>
  )
}
