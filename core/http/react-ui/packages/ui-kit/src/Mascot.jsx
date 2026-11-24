import welcome from './assets/mascot-welcome.webp'
import circle from './assets/mascot-circle.webp'

// The LocalAI llama, for the places where the app has something friendly to
// say: a first run, an empty list, a page that is not there. It is a picture
// beside the words, never the only carrier of meaning, so it is hidden from
// assistive technology and has no animation.
//
//   pose   which picture: "welcome" (the bust) or "circle" (the whole llama
//          on its own teal ground, 208 px square for a 104 px badge). Any
//          other pose falls back to "welcome", so a call can already name the
//          pose it wants and the art can arrive later (see POSES).
//   size   "sm" (72 px high), "md" (112 px, default), "lg" (160 px) or
//          "badge" (104 px, for the circle)
//
// The circle is LocalAI brand art. An app that lets an operator rebrand it
// shows the circle only for the bundled brand.
//
// Do not put it on dense operator pages (nodes, traffic, traces, logs) or on
// a state where an error needs attention.
export const POSES = { welcome, circle }

export default function Mascot({ pose = 'welcome', size = 'md', className = '' }) {
  const src = POSES[pose] || POSES.welcome
  return (
    <img
      className={`ui-mascot ui-mascot--${size} ${className}`.trim()}
      src={src}
      alt=""
      aria-hidden="true"
      draggable="false"
      decoding="async"
      data-pose={pose}
    />
  )
}
