// Returns an inline style setting --reveal-index for orchestrated reveals.
// Usage: <div className="reveal-stagger"> {items.map((it, i) => (
//   <Row key={it.id} style={staggerStyle(i)} />))} </div>
// Provided as a plain helper (not a hook) so it can be called in a map
// without violating rules-of-hooks. File name kept for discoverability.
export function staggerStyle(index) {
  return { '--reveal-index': index }
}
