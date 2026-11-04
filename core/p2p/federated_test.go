// SPDX-License-Identifier: MIT

package p2p

import (
	"net"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Federated connection balancing", func() {
	var fs *FederatedServer
	addNode := func(id string) {
		AddNode("federated-test", schema.NodeData{ID: id, LastSeen: time.Now()})
	}

	BeforeEach(func() {
		fs = NewFederatedServer(":0", "federated-test", "", true, "")
	})
	AfterEach(func() {
		mu.Lock()
		delete(nodes, "federated-test")
		mu.Unlock()
	})

	It("selects an idle node instead of a busy node", func() {
		addNode("idle")
		addNode("busy")
		for range 100 {
			fs.requestTable = map[string]int{"idle": 0, "busy": 50}
			Expect(fs.SelectLeastUsedServer()).To(Equal("idle"))
		}
	})

	It("reserves capacity atomically for overlapping connections", func() {
		for _, id := range []string{"a", "b", "c", "d"} {
			addNode(id)
			fs.requestTable[id] = 10
		}
		fs.requestTable["c"] = 9
		selected := make(chan string, 33)
		var wg sync.WaitGroup
		for range 33 {
			wg.Go(func() { selected <- fs.SelectLeastUsedServer() })
		}
		wg.Wait()
		close(selected)
		counts := map[string]int{}
		for id := range selected {
			counts[id]++
		}
		Expect(counts).To(Equal(map[string]int{"a": 8, "b": 8, "c": 9, "d": 8}))
	})

	It("releases capacity without accumulating lifetime traffic", func() {
		addNode("old")
		for range 1000 {
			id := fs.SelectLeastUsedServer()
			Expect(id).To(Equal("old"))
			fs.ReleaseRequest(id)
		}
		addNode("new")
		first := fs.SelectLeastUsedServer()
		second := fs.SelectLeastUsedServer()
		Expect([]string{first, second}).To(ConsistOf("old", "new"))
		fs.ReleaseRequest(first)
		fs.ReleaseRequest(second)
		Expect(fs.requestTable).To(BeEmpty())
	})

	It("holds a reservation during forwarding and releases it on return", func() {
		AddNode(fs.service, schema.NodeData{ID: "a", ServiceID: "service-a", LastSeen: time.Now()})
		server, client := net.Pipe()
		defer func() { _ = server.Close() }()
		defer func() { _ = client.Close() }()
		called := false
		fs.handleConnection(server, func(serviceID string, conn net.Conn) {
			called = true
			Expect(serviceID).To(Equal("service-a"))
			Expect(conn).To(Equal(server))
			addNode("b")
			// Another connection must see the reservation before forwarding ends.
			Expect(fs.SelectLeastUsedServer()).To(Equal("b"))
			fs.ReleaseRequest("b")
		})
		Expect(called).To(BeTrue())
		Expect(fs.requestTable).To(BeEmpty())
	})

	It("keeps active reservations when a node goes offline and returns", func() {
		addNode("a")
		Expect(fs.SelectLeastUsedServer()).To(Equal("a"))
		Expect(fs.SelectLeastUsedServer()).To(Equal("a"))
		AddNode(fs.service, schema.NodeData{ID: "a", LastSeen: time.Now().Add(-time.Hour)})
		Expect(fs.SelectLeastUsedServer()).To(BeEmpty())
		Expect(fs.RandomServer()).To(BeEmpty())
		addNode("a")
		addNode("b")
		Expect(fs.SelectLeastUsedServer()).To(Equal("b"))
		fs.ReleaseRequest("a")
		fs.ReleaseRequest("a")
		Expect(fs.SelectLeastUsedServer()).To(Equal("a"))
		fs.ReleaseRequest("a")
		fs.ReleaseRequest("b")
		Expect(fs.requestTable).To(BeEmpty())
	})

	It("returns no selection when no nodes are available", func() {
		Expect(fs.SelectLeastUsedServer()).To(BeEmpty())
		Expect(fs.RandomServer()).To(BeEmpty())
		fs.ReleaseRequest("unknown")
		Expect(fs.requestTable).To(BeEmpty())
	})

	It("selects randomly among equally loaded nodes", func() {
		addNode("a")
		addNode("b")
		seen := map[string]bool{}
		for range 128 {
			id := fs.SelectLeastUsedServer()
			seen[id] = true
			fs.ReleaseRequest(id)
		}
		Expect(seen).To(Equal(map[string]bool{"a": true, "b": true}))
	})

	It("allows concurrent selection, release, and random routing", func() {
		addNode("online")
		AddNode(fs.service, schema.NodeData{ID: "offline"})
		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				defer GinkgoRecover()
				for range 20 {
					id := fs.SelectLeastUsedServer()
					Expect(id).To(Equal("online"))
					Expect(fs.RandomServer()).To(Equal("online"))
					fs.ReleaseRequest(id)
				}
			})
		}
		wg.Wait()
		Expect(fs.requestTable).To(BeEmpty())
	})
})
