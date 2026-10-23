// The one page of the Voice add-on. Its sections are tabs: Recognition,
// Library, Text to speech and From a recording. The tab is the first segment of
// the page path (voice/library), the same convention the Studio add-on uses;
// the profile form is a view of the Library tab (voice/library/new).
const DEFAULT_TAB = 'recognition'
const TABS = ['recognition', 'library', 'speech', 'recording']

export function createVoiceHub(ctx, api, identity, sections) {
  const { React, h, Fragment, t } = ctx
  const { useEffect, useMemo, useState } = React

  // Where an older page address goes now. `search` is the query string the
  // old address carried (a model, a selected profile, a voice); it is kept.
  function redirectFor(legacy, path, search = '') {
    const params = new URLSearchParams(search)
    let tab = legacy
    let rest = path
    if (legacy === 'recognition' && params.get('tab') === 'recording') { tab = 'recording'; rest = '' }
    if (legacy === 'recognition') params.delete('tab')
    if (legacy === 'create') { tab = 'library'; rest = 'new' }
    const query = params.toString()
    return `voice/${tab}${rest ? `/${rest}` : ''}${query ? `?${query}` : ''}`
  }

  function Hub({ path }) {
    const segments = String(path || '').split('/').filter(Boolean)
    const tab = TABS.includes(segments[0]) ? segments[0] : DEFAULT_TAB
    const rest = segments.slice(tab === segments[0] ? 1 : 0).join('/')
    const isAdmin = ctx.viewer.isAdmin()

    // The counts on the tabs. The people this browser knows come from the
    // recognition tab; the saved voices are read once for an administrator and
    // then follow the library tab.
    const [speakers, setSpeakers] = useState(() => identity.loadList('localai_voice_enrollments').length)
    const [library, setLibrary] = useState({ loading: true, count: 0 })
    useEffect(() => {
      if (!isAdmin) return undefined
      let live = true
      api.voiceProfiles.list()
        .then((body) => { if (live) setLibrary({ loading: false, count: (body?.data || []).length }) })
        .catch(() => { if (live) setLibrary({ loading: false, count: 0 }) })
      return () => { live = false }
    }, [isAdmin])
    const onLibraryCount = useMemo(() => (count) => setLibrary({ loading: false, count }), [])

    const label = {
      recognition: <>{t('voices.tab.recognition')} <span className="dk-hubtab-count">{speakers}</span></>,
      library: <>{t('voices.tab.library')} <span className="dk-hubtab-count">{library.loading ? '' : library.count}</span></>,
      speech: t('voices.tab.speech'),
      recording: t('voices.tab.recording'),
    }
    const shown = TABS.filter((key) => key !== 'library' || isAdmin)
    const tabs = (
      <nav className="dk-tabs idn-tabs vc-tabs" role="tablist" aria-label={t('voices.tabs')} data-testid="voice-tabs">
        {shown.map((key) => (
          <ctx.Link key={key} className="dk-tab" role="tab" page={`voice/${key}`} data-tab={key} aria-selected={tab === key} aria-current={tab === key ? 'page' : undefined}>
            {label[key]}
          </ctx.Link>
        ))}
      </nav>
    )

    if (tab === 'library') {
      const Page = rest === 'new' ? sections.create : sections.library
      return <Page tabs={tabs} onLibraryCount={onLibraryCount} />
    }
    if (tab === 'speech') return <sections.speech path={rest} tabs={tabs} />
    return <sections.recognition path={rest} tab={tab === 'recording' ? 'recording' : 'speakers'} tabs={tabs} onSpeakerCount={setSpeakers} />
  }

  return { Hub, redirectFor }
}
