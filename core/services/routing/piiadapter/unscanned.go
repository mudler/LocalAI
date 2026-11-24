package piiadapter

// blockKinds collects, in order and without duplicates, the "type" of every
// structured content block that is not text, i.e. content the text detectors
// never see. textTypes lists the block types that carry no media of their own.
func blockKinds(blocks []any, textTypes map[string]bool, seen map[string]bool, out []string) []string {
	for _, block := range blocks {
		m, ok := block.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		if t == "" || textTypes[t] || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
