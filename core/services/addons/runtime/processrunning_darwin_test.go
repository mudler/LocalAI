//go:build darwin

// SPDX-License-Identifier: MIT
package runtime

import (
	"errors"
	"io/fs"
	"time"

	. "github.com/onsi/gomega"
)

func processRunning(pid int) bool {
	_, stat, e := procState(pid)
	if errors.Is(e, fs.ErrNotExist) {
		return false
	}
	Expect(e).NotTo(HaveOccurred())
	return stat != szomb
}

// startBudget is how long the specs wait for an add-on to become ready:
// the first exec of a new executable takes about half a second to be admitted by the system.
const startBudget = 5 * time.Second
