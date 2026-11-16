// SPDX-License-Identifier: MIT
package schema

// SkinTokensParameters is shared by discovery and the native backend's admission
// checks. Defaults mirror the pinned upstream generation options.
func SkinTokensParameters(operation string) []ThreeDParameter {
	if operation == "retarget" {
		return nil
	}
	params := []ThreeDParameter{
		{Name: "seed", Label: "Seed", Type: "uint64", Default: "0", Advanced: true},
		{Name: "top_k", Label: "Top K", Type: "integer", Default: "5", Min: 1, Max: 1024, Advanced: true},
		{Name: "top_p", Label: "Top P", Type: "number", Default: "0.95", Min: 0.001, Max: 1, Advanced: true},
		{Name: "temperature", Label: "Temperature", Type: "number", Default: "1", Min: 0.01, Max: 5, Advanced: true},
		{Name: "repetition_penalty", Label: "Repetition penalty", Type: "number", Default: "2", Min: 0.01, Max: 10, Advanced: true},
		{Name: "beams", Label: "Beams", Type: "integer", Default: "10", Min: 1, Max: 32, Advanced: true},
		{Name: "max_tokens", Label: "Maximum tokens", Type: "integer", Default: "2048", Min: 8, Max: 65536, Advanced: true},
	}
	if operation == "skin" {
		params = append(params, ThreeDParameter{Name: "fit", Label: "Skeleton fit", Type: "enum", Default: "global", Options: []string{"none", "global"}})
	}
	return params
}
