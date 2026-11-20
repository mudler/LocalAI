// SPDX-License-Identifier: MIT
package schema

type Graph struct {
	Nodes   []Node  `json:"nodes"`
	Primary string  `json:"primary"`
	Target  Profile `json:"target"`
}
