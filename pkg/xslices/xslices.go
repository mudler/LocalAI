// SPDX-License-Identifier: MIT

// Package xslices holds small helpers for slices that the standard library
// does not have.
package xslices

// NonNil returns s, or an empty list when s is nil, so a JSON encoder writes
// [] and not null.
func NonNil[S ~[]E, E any](s S) S {
	if s == nil {
		return S{}
	}
	return s
}
