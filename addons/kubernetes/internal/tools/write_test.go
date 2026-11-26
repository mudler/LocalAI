// SPDX-License-Identifier: MIT
package tools_test

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	"github.com/mudler/LocalAI/addons/kubernetes/kubetest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

// kubeconfigFor is the fixture kubeconfig for another server.
func kubeconfigFor(server string) string {
	return strings.Replace(kubeconfig, "server: https://cluster.example:6443", "server: "+server, 1)
}

var _ = Describe("the apply step of a write", func() {
	It("writes an operation once when the host applies it many times at once", func() {
		g := newRig(deployment("team-a", "web", 2))
		p, err := g.preview("op", tools.ToolScale, scaleArgs)
		Expect(err).NotTo(HaveOccurred())
		e := g.env("op", tools.ToolScale, scaleArgs)
		var wg sync.WaitGroup
		var committed, unknown atomic.Int32
		for range 8 {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				rc, err := g.rt.Apply(ctx, e, p.Revision)
				Expect(err).NotTo(HaveOccurred())
				switch rc.State {
				case contract.ReceiptCommitted:
					committed.Add(1)
				case contract.ReceiptUnknown:
					unknown.Add(1)
				}
			}()
		}
		wg.Wait()
		Expect(committed.Load() + unknown.Load()).To(BeNumerically("==", 8))
		Expect(committed.Load()).To(BeNumerically(">=", 1))
		patches := 0
		for _, a := range g.k.Dyn.Actions() {
			if a.GetVerb() == "patch" {
				patches++
			}
		}
		Expect(patches).To(Equal(1), "the scale was written once")
	})

	It("writes an operation once when two applies pass the first journal check together", func() {
		g := newRig(deployment("team-a", "web", 2))
		p, err := g.preview("op", tools.ToolScale, scaleArgs)
		Expect(err).NotTo(HaveOccurred())
		e := g.env("op", tools.ToolScale, scaleArgs)

		// Both applies read the journal, find nothing, and then wait inside
		// inspect until both are there. Only the check of the journal under
		// the lock, after inspect, keeps the second one from writing. Each
		// apply talks to its own fake cluster with the same objects, since
		// a fake cluster runs one reactor at a time.
		var block atomic.Bool
		arrived, release := make(chan struct{}, 2), make(chan struct{})
		var clusters []*kubetest.Cluster
		for range 2 {
			k := kubetest.New(deployment("team-a", "web", 2))
			k.Prepend("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
				if block.Load() {
					arrived <- struct{}{}
					<-release
				}
				return false, nil, nil
			})
			clusters = append(clusters, k)
		}
		var next atomic.Int32
		g.rt.Factory = func(*rest.Config) (*kubeapi.Client, error) {
			return clusters[int(next.Add(1)-1)%2].Client(), nil
		}
		block.Store(true)
		states := make(chan contract.ReceiptState, 2)
		for range 2 {
			go func() {
				defer GinkgoRecover()
				rc, err := g.rt.Apply(ctx, e, p.Revision)
				Expect(err).NotTo(HaveOccurred())
				states <- rc.State
			}()
		}
		Eventually(arrived).Should(Receive())
		Eventually(arrived).Should(Receive())
		block.Store(false)
		close(release)
		var got []contract.ReceiptState
		for range 2 {
			var st contract.ReceiptState
			Eventually(states, 5*time.Second).Should(Receive(&st))
			got = append(got, st)
		}
		Expect(got).To(ConsistOf(contract.ReceiptCommitted, contract.ReceiptUnknown))
		patches := 0
		for _, k := range clusters {
			for _, a := range k.Dyn.Actions() {
				if a.GetVerb() == "patch" {
					patches++
				}
			}
		}
		Expect(patches).To(Equal(1), "the scale was written once")
	})

	It("does not hold up a write to another cluster while one cluster is slow", func() {
		slow := kubetest.New(deployment("team-a", "web", 2))
		fast := kubetest.New(deployment("team-a", "web", 2))
		g := newRig()
		g.rt.Factory = func(cfg *rest.Config) (*kubeapi.Client, error) {
			if strings.Contains(cfg.Host, "slow.example") {
				return slow.Client(), nil
			}
			return fast.Client(), nil
		}
		slowCred := kubeconfigFor("https://slow.example:6443")
		slowKey := "https://slow.example:6443#context=dev"
		slowEnv := func(op string) contract.Envelope {
			e := g.env(op, tools.ToolScale, map[string]any{"kind": "Deployment", "namespace": "team-a", "name": "web", "replicas": 3})
			e.Request.Credential, e.Request.ResourceKey = slowCred, slowKey
			return e
		}
		_, err := g.pol.Set(slowKey, g.mustPolicy())
		Expect(err).NotTo(HaveOccurred())
		p, err := g.rt.Preview(ctx, slowEnv("slow-op"))
		Expect(err).NotTo(HaveOccurred())

		var block atomic.Bool
		blocked, release := make(chan struct{}, 1), make(chan struct{})
		slow.Prepend("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
			if block.Load() {
				select {
				case blocked <- struct{}{}:
				default:
				}
				<-release
			}
			return false, nil, nil
		})
		block.Store(true)
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			_, err := g.rt.Apply(ctx, slowEnv("slow-op"), p.Revision)
			Expect(err).NotTo(HaveOccurred())
		}()
		Eventually(blocked).Should(Receive())

		fastDone := make(chan contract.Receipt, 1)
		go func() {
			defer GinkgoRecover()
			fp, err := g.preview("fast-op", tools.ToolScale, scaleArgs)
			Expect(err).NotTo(HaveOccurred())
			rc, err := g.apply("fast-op", tools.ToolScale, scaleArgs, fp)
			Expect(err).NotTo(HaveOccurred())
			fastDone <- rc
		}()
		var rc contract.Receipt
		Eventually(fastDone, 5*time.Second).Should(Receive(&rc), "the write to the other cluster waits for the slow one")
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		close(release)
		Eventually(done, 5*time.Second).Should(BeClosed())
	})
})
