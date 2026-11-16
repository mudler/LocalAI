// SPDX-License-Identifier: MIT
import { connectionLanes } from './state.js'

export function createChainConnectors(React) {
  const h = React.createElement
  const { useEffect, useRef, useState } = React
  return function ChainConnectors({ graph, children }) {
    const container = useRef(null)
    const [paths, setPaths] = useState([])
    const lanes = connectionLanes(graph)
    const gutterWidth = 28 + Math.max(0, ...lanes.map(edge => edge.lane)) * 12
    const connections = JSON.stringify(lanes)
    useEffect(() => {
      const root = container.current
      if (!root) return
      let frame
      const measure = () => {
        const bounds = root.getBoundingClientRect()
        const cards = new Map([...root.querySelectorAll('[data-node]')].map(card => [card.dataset.node, card.getBoundingClientRect()]))
        const edges = JSON.parse(connections)
        const next = edges.flatMap(({ from, to, lane }) => {
          const a = cards.get(from), b = cards.get(to)
          if (!a || !b) return []
          const outgoing = edges.filter(edge => edge.from === from).findIndex(edge => edge.to === to)
          const incoming = edges.filter(edge => edge.to === to).findIndex(edge => edge.from === from)
          const x1 = a.right - bounds.left, y1 = a.bottom - bounds.top - 22 - outgoing * 12
          const x2 = b.right - bounds.left, y2 = b.top - bounds.top + 24 + incoming * 12
          const gutter = Math.max(x1, x2) + 20 + lane * 12
          return [{ from, to, d: `M ${x1} ${y1} H ${gutter} V ${y2} H ${x2 + 2}`, head: `M ${x2 + 8} ${y2 - 5} L ${x2 + 2} ${y2} L ${x2 + 8} ${y2 + 5}` }]
        })
        setPaths(previous => JSON.stringify(previous) === JSON.stringify(next) ? previous : next)
      }
      const schedule = () => { cancelAnimationFrame(frame); frame = requestAnimationFrame(measure) }
      const observer = new ResizeObserver(schedule)
      observer.observe(root)
      root.querySelectorAll('[data-node]').forEach(card => observer.observe(card))
      window.addEventListener('resize', schedule)
      schedule()
      return () => { observer.disconnect(); cancelAnimationFrame(frame); window.removeEventListener('resize', schedule) }
    }, [connections])
    return <div className="ap-chain" ref={container} style={{ paddingRight: gutterWidth }}>
      <svg className="ap-connections" aria-hidden="true" focusable="false">{paths.map(path => <g key={`${path.from}-${path.to}`} data-connection={`${path.from}:${path.to}`}><path d={path.d} /><path d={path.head} /></g>)}</svg>
      {children}
    </div>
  }
}
