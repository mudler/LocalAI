import { useRef, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import {
  Button, Callout, EmptyState, LoadError, Page, PageHeader, PaneLayout, SidePane,
  Skeleton, SkeletonRows, LoadingSpinner, ShellFallback, StatusChip, STATUS, Switch,
} from '../components/ui'
import { useToastAnchor } from '../components/Toast'
import StatusPill from '../components/StatusPill'
import './ui-catalog.css'

// Every UI primitive in every state, on one page. It is the reference for
// the slices that move pages onto the primitives, and the fixture of
// e2e/ui-primitives.spec.js. It is not in the navigation. The text is
// sample content, so it is not translated.

const VARIANTS = ['primary', 'secondary', 'ghost', 'danger', 'danger-ghost']

function Section({ id, title, note, children }) {
  return (
    <section className="ui-cat__section" id={id} data-testid={`cat-section-${id}`}>
      <h2 className="ui-cat__title">{title}</h2>
      {note && <p className="ui-cat__note">{note}</p>}
      {children}
    </section>
  )
}

function Buttons() {
  return (
    <Section id="buttons" title="Button" note="One button: the kit's dk-btn. Legacy .btn classes render the same look.">
      <div className="ui-cat__table" role="table" aria-label="Button variants">
        {VARIANTS.map(v => (
          <div className="ui-cat__row" role="row" key={v} data-variant={v}>
            <span className="ui-cat__label" role="cell">{v}</span>
            <span className="ui-cat__cell" role="cell">
              <Button variant={v} data-testid={`btn-${v}`}>{v === 'danger' ? 'Delete model' : v === 'danger-ghost' ? 'Remove…' : 'Add server'}</Button>
              <Button variant={v} icon="plus">With icon</Button>
              <Button variant={v} size="sm" data-testid={`btn-${v}-sm`}>Small</Button>
              <Button variant={v} icon="settings" aria-label="Settings" />
              <Button variant={v} icon="settings" size="sm" aria-label="Settings" />
              <Button variant={v} disabled>Disabled</Button>
              <Button variant={v} busy icon="refresh">Saving</Button>
              <Button variant={v} busy data-testid={`btn-${v}-busy`}>Saving</Button>
            </span>
          </div>
        ))}
        <div className="ui-cat__row" role="row">
          <span className="ui-cat__label" role="cell">link</span>
          <span className="ui-cat__cell" role="cell">
            <Button to="/app/ui-catalog#buttons" variant="secondary" icon="arrow-right">Router link</Button>
            <Button href="#buttons" variant="ghost">Anchor</Button>
            <Button size="lg" variant="primary">Large</Button>
          </span>
        </div>
      </div>
      <h3 className="ui-cat__sub">Legacy classes, unchanged markup</h3>
      <div className="ui-cat__cell" data-testid="legacy-buttons">
        <button type="button" className="btn btn-primary" data-testid="legacy-primary">btn-primary</button>
        <button type="button" className="btn btn-secondary" data-testid="legacy-secondary">btn-secondary</button>
        <button type="button" className="btn btn-ghost">btn-ghost</button>
        <button type="button" className="btn btn-danger">btn-danger</button>
        <button type="button" className="btn btn-danger-ghost">btn-danger-ghost</button>
        <button type="button" className="btn btn-secondary btn-sm" data-testid="legacy-sm">btn-sm</button>
        <button type="button" className="btn" data-testid="legacy-plain">btn</button>
      </div>
    </Section>
  )
}

function Header() {
  return (
    <Section id="header" title="Page frame and header" note="Title 28/34, a supporting line of up to 64 characters, actions at the bottom right, 24 px to the content.">
      <div className="ui-cat__frame">
        <PageHeader
          title="Agents"
          supporting="Saved agents: instructions, a model, and what a chat with them may use."
          actions={<><Button variant="ghost" icon="import">Import</Button><Button variant="primary" icon="plus">New agent</Button></>}
        />
        <p className="ui-cat__note">The content starts here.</p>
      </div>
    </Section>
  )
}

function Chips() {
  return (
    <Section id="chips" title="Status chip" note="Colour, icon and words. One state per chip.">
      <div className="ui-cat__cell" data-testid="chips">
        {Object.keys(STATUS).map(s => <StatusChip key={s} status={s} />)}
      </div>
      <div className="ui-cat__cell">
        {Object.keys(STATUS).map(s => <StatusChip key={s} status={s} dot />)}
      </div>
      <div className="ui-cat__cell">
        <StatusChip tone="warn">Low disk</StatusChip>
        <StatusChip tone="idle">Built-in</StatusChip>
        <StatusChip tone="accent">New</StatusChip>
        <StatusChip status="healthy" label="healthy (alias)" />
        <span data-testid="legacy-pill"><StatusPill status="degraded" /></span>
      </div>
    </Section>
  )
}

function Switches() {
  const [on, setOn] = useState(true)
  return (
    <Section id="switch" title="Switch" note="A control that applies at once. Space toggles. Name it with a label.">
      <div className="ui-cat__cell" data-testid="switches">
        <Switch checked={on} onChange={setOn} label="Sample switch" />
        <Switch checked={!on} onChange={() => setOn(v => !v)} label="Opposite switch" />
        <Switch checked={false} disabled label="Disabled off" />
        <Switch checked disabled label="Disabled on" />
      </div>
    </Section>
  )
}

function Callouts() {
  return (
    <Section id="callouts" title="Callout">
      <div className="ui-cat__stack">
        <Callout tone="info" title="Chats use the connectors that are on.">A saved agent uses only the ones you pick for it.</Callout>
        <Callout tone="ok" title="qwen3-8b is ready." actions={<Button size="sm" variant="primary">Start a chat</Button>}>It loaded in 2.1 s.</Callout>
        <Callout tone="warn" title="Tables 1.0.0 is available." actions={<><Button size="sm" variant="ghost">What changed</Button><Button size="sm">Update</Button></>}>You have 0.9.1. It asks for one more permission.</Callout>
        <Callout tone="error" title="research-assistant failed" actions={<Button size="sm">Open the run</Button>}>in “Digest of the week”, 12 minutes ago. The model stopped before it answered.</Callout>
      </div>
    </Section>
  )
}

function LoadErrors() {
  const [state, setState] = useState('error')
  const [tries, setTries] = useState(0)
  const retry = () => new Promise(resolve => setTimeout(() => {
    setTries(n => n + 1)
    setState('ready')
    resolve()
  }, 600))
  return (
    <Section id="load-error" title="Load error" note="Replaces the region that failed. Never the empty state, never a bare toast.">
      <div className="ui-cat__stack">
        {state === 'error'
          ? <LoadError title="Could not load your MCP servers." error={{ message: 'database is locked' }} onRetry={retry} testId="cat-load-error" />
          : (
            <div className="ui-cat__stack" data-testid="cat-load-ready">
              <Callout tone="ok" title="Loaded 3 servers.">{`After ${tries} ${tries === 1 ? 'retry' : 'retries'}.`}</Callout>
              <Button size="sm" variant="ghost" onClick={() => setState('error')}>Fail again</Button>
            </div>
          )}
        <LoadError title="Add-ons are not available right now." error="The add-on service did not answer." onRetry={() => {}} />
        <LoadError title="Could not load the log." nothingChanged={false} />
      </div>
    </Section>
  )
}

function Empties() {
  return (
    <Section id="empty" title="Empty state">
      <div className="ui-cat__stack">
        <EmptyState
          testId="cat-empty-first-run"
          title="No MCP servers yet"
          body="Add the address of a server this machine can reach over http or https. If it needs a key, add it as a token or a header."
          facts={[{ text: 'Token or custom header' }, { text: 'OAuth sign-in (not yet)', ok: false }]}
          actions={<><Button variant="primary" icon="plus">Add MCP server</Button><Button variant="ghost" icon="book">How MCP works</Button></>}
        />
        <EmptyState
          icon="memory"
          title="Nothing remembered yet"
          body="Memory keeps what your chats should know next time."
          steps={['Turn on memory for a chat.', 'Ask it to remember something.', 'Find it here, and edit or remove it.']}
          actions={<Button variant="primary">Turn on memory</Button>}
        />
        <EmptyState variant="no-results" title="No agents match “digest”." actions={<Button variant="ghost" size="sm">Clear filters</Button>} />
        <EmptyState variant="unavailable" title="Triggers are off on this server." actions={<Button variant="ghost" size="sm" iconEnd="arrow-right">Settings</Button>} />
      </div>
    </Section>
  )
}

function Loading() {
  return (
    <Section id="loading" title="Skeleton and spinner" note="Skeletons for content that is coming (after 200 ms), a spinner for a short wait.">
      <SkeletonRows count={3} testId="cat-skeleton-rows" />
      <div className="ui-cat__cell ui-cat__cell--loose">
        <span className="ui-cat__skel"><Skeleton variant="title" /><Skeleton count={2} /></span>
        <Skeleton variant="circle" />
        <LoadingSpinner size="sm" />
        <LoadingSpinner />
        <LoadingSpinner size="lg" />
      </div>
      <h3 className="ui-cat__sub">Boot shell</h3>
      <ShellFallback embedded />
    </Section>
  )
}

function Toasts() {
  const { addToast } = useOutletContext() || {}
  const [undone, setUndone] = useState(0)
  const [bar, setBar] = useState(false)
  return (
    <Section id="toasts" title="Toast" note="For the result of an action the person just took. 4 s, 8 s with an action, errors stay.">
      <div className="ui-cat__cell">
        <Button data-testid="toast-info" onClick={() => addToast?.('Settings saved.', 'success')}>Success</Button>
        <Button onClick={() => addToast?.('Check the model name.', 'warning')}>Warning</Button>
        <Button onClick={() => addToast?.('Could not delete the file: it is in use.', 'error')}>Error</Button>
        <Button onClick={() => addToast?.('2 models are loading.', 'info')}>Info</Button>
        <Button
          variant="primary"
          data-testid="toast-undo"
          onClick={() => addToast?.('Deleted “daily-digest”.', 'success', undefined, {
            testId: 'cat-undo-toast',
            action: { label: 'Undo', onClick: () => setUndone(n => n + 1) },
          })}
        >
          Delete with Undo
        </Button>
        <Button variant="ghost" data-testid="toast-bar" onClick={() => setBar(b => !b)}>{bar ? 'Hide' : 'Show'} a bottom bar</Button>
      </div>
      <p className="ui-cat__note" data-testid="undo-count">Undo clicked: {undone}</p>
      {bar && <BottomBar />}
    </Section>
  )
}

// Stands in for the chat composer: a bar fixed to the bottom of the window
// that the toast stack must clear.
function BottomBar() {
  const ref = useRef(null)
  useToastAnchor(ref)
  return <div ref={ref} className="ui-cat__bottombar" data-testid="cat-bottom-bar">A bar fixed at the bottom, like the chat composer.</div>
}

function Pane() {
  const [open, setOpen] = useState(false)
  return (
    <Section id="pane" title="Side pane" note="Docked from 1280 px, an overlay below, a bottom sheet on a phone. Escape closes it.">
      <PaneLayout>
        <div className="ui-cat__stack">
          <div className="ui-cat__list">
            {['Tables', 'Office', 'Voice'].map(name => (
              <div className="ui-cat__item" key={name}>
                <strong>{name}</strong>
                <StatusChip status={name === 'Office' ? 'failed' : 'running'} />
                <Button size="sm" onClick={() => setOpen(true)} data-testid={`open-pane-${name}`}>Details</Button>
              </div>
            ))}
          </div>
        </div>
        <SidePane
          open={open}
          onClose={() => setOpen(false)}
          title="Tables"
          meta={<><StatusChip status="running" /><span>1.0.0 · Main library</span></>}
          footer={<><Button variant="danger-ghost">Remove Tables…</Button><Button variant="primary">Open Tables</Button></>}
          testId="cat-pane"
        >
          <div className="ui-cat__stack">
            <Callout tone="warn" title="Update available.">1.0.1 fixes a crash on import.</Callout>
            <p className="ui-cat__note">Spreadsheets your chats can read and change. The body scrolls; the header and the footer stay.</p>
            {Array.from({ length: 12 }).map((_, i) => <p key={i} className="ui-cat__note">Line {i + 1} of the pane body.</p>)}
          </div>
        </SidePane>
      </PaneLayout>
    </Section>
  )
}

export default function UiCatalog() {
  return (
    <Page className="ui-cat">
      <PageHeader title="UI primitives" supporting="Every shared component in every state. The reference for pages that move onto them." />
      <Buttons />
      <Header />
      <Chips />
      <Switches />
      <Callouts />
      <LoadErrors />
      <Empties />
      <Loading />
      <Toasts />
      <Pane />
    </Page>
  )
}
