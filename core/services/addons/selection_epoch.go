// SPDX-License-Identifier: MIT
package addons

// SelectionEpoch detects candidate-set ABA within this owner lifetime. It
// changes at lifecycle publication, runtime ownership changes and recovery
// quiescence, never on reads. A binding must also check current incarnation:
// the supervisor can restart a process without a Manager mutation. The epoch
// is per owner, not persisted or comparable across owner restarts.
func (m *Manager) SelectionEpoch() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.selectionEpoch
}
