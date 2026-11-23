package upgraderun_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestUpgradeRun(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Upgrade run Suite")
}
