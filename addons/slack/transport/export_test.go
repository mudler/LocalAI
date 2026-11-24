// SPDX-License-Identifier: MIT
package transport

import "time"

var _ time.Duration


// StateFileOf is the file in which the link of tokens keeps its followed threads.
func StateFileOf(dir string, t Tokens) string { return stateFile(dir, keyOf(t)) }

// ThreadLife is how long a followed thread stays followed without a message.
const ThreadLife = threadLife

