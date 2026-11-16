// SPDX-License-Identifier: MIT
import { ancestors } from './state.js'

export const ANIMATION_PREFERENCE = 'localai_asset_pipeline_animation'
export const ANIMATION_LABEL = 'Animation (WIP)'
const ANIMATION_KINDS = new Set(['rig', 'skin', 'motion', 'retarget'])

export function isAnimationNode(node) {
  return ANIMATION_KINDS.has(node.kind)
    || node.id === 'motion-description'
    || (node.kind === 'review' && node.params?.purpose === 'motion')
}

// This is a browser UI preference, not an owner policy or a graph mutation.
export function readAnimationPreference() {
  try { return localStorage.getItem(ANIMATION_PREFERENCE) === 'true' } catch { return false }
}

export function writeAnimationPreference(enabled) {
  try { localStorage.setItem(ANIMATION_PREFERENCE, String(enabled)) } catch { /* Still usable for this visit. */ }
}

export function animationIssue(graph, enabled, target = graph?.primary) {
  if (enabled || !graph || !target) return ''
  const active = ancestors(graph, target)
  return graph.nodes.some(node => active.has(node.id) && isAnimationNode(node))
    ? 'Enable Animation (WIP) to run this pipeline, or choose a non-animation primary output.'
    : ''
}
