// SplitView is the shell the resource lifecycle pages share: a rail you scan on the
// left, a pane that answers on the right.
//
// It exists because the old Models and Backends tables had the same defect - an
// eight-column table over a click-to-expand row - and the fix is the same
// shape every time. What differs between them is what the rail lists and what
// the pane says when nothing is selected, so those are the props.
//
// `detail` is not decoration. Below the breakpoint the two columns cannot both
// survive, and a selected entity means the pane is the page, so the rail steps
// aside. That is the trade a pushed route would make, without the routing.
export default function SplitView({ rail, pane, detail = false, testId }) {
  return (
    <div className={`split-view${detail ? ' split-view--detail' : ''}`} data-testid={testId}>
      <div className="split-view__rail-col">{rail}</div>
      <div className="split-view__pane" data-testid={testId ? `${testId}-pane` : undefined}>
        {pane}
      </div>
    </div>
  )
}
