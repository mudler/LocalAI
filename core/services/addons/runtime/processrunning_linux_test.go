//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"os"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/gomega"
)

func processRunning(pid int) bool {
	b, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if os.IsNotExist(e) {
		return false
	}
	Expect(e).NotTo(HaveOccurred())
	fields := strings.Fields(string(b)[strings.LastIndex(string(b), ")")+1:])
	return fields[0] != "Z" && fields[0] != "X"
}

// startBudget is how long the specs wait for an add-on to become ready:
// a fixture starts in a few milliseconds.
const startBudget = 300 * time.Millisecond
