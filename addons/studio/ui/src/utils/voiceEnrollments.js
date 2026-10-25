// SPDX-License-Identifier: MIT
const ENROLL_KEY = 'localai_voice_enrollments'

export function loadEnrollments() {
  try {
    const raw = localStorage.getItem(ENROLL_KEY)
    if (!raw) return []
    const p = JSON.parse(raw)
    return Array.isArray(p) ? p : []
  } catch (_) { return [] }
}

export function saveEnrollments(list) {
  try { localStorage.setItem(ENROLL_KEY, JSON.stringify(list.slice(0, 50))) } catch (_) { /* quota */ }
}

// Only registration metadata belongs in the browser's management list.
export function rememberEnrollment({ id, name, registered_at }) {
  saveEnrollments([{ id, name, registeredAt: registered_at, labels: {} }, ...loadEnrollments()])
}
