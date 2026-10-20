// SPDX-License-Identifier: MIT
package foundation_test

import (
	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Two separate server processes, each with its own listener and carrier, share
// one account store and forward every add-on request to the one owner.
var _ = Describe("two non-owner frontends with real sessions", Ordered, func() {
	var (
		c   *cluster.Cluster
		res string
	)
	nodes := []string{cluster.Owner, cluster.FrontA, cluster.FrontB}

	BeforeAll(func() {
		c, _ = serving([]cluster.Package{cluster.ToolPackage("1.0.0", nil)}, map[string]string{"found-tool": "1.0.0"})
		res = resource(c, "found-tool", "member", nil)
	})

	It("gives an ordinary account the apps list and no management on any node", func() {
		for _, n := range nodes {
			member := c.Session(n, "member")
			status, list := apps(member)
			Expect(status).To(Equal(200), n)
			Expect(list).To(HaveLen(1))
			raw, err := member.Do("GET", "/api/addons/apps", nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(raw.String()).NotTo(ContainSubstring(`"admin"`), n+": the apps list carries no controls for a member")
			for _, action := range []string{"install", "upgrade", "enable", "disable", "uninstall", "catalog-sources", "addons-installed"} {
				r := dispatch(member, map[string]any{"addonId": "found-tool", "action": action, "generation": 1, "arguments": map[string]any{}})
				Expect(r.Status).To(Equal(403), n+" "+action+": "+r.String())
			}
			for _, verb := range []string{"start", "stop"} {
				r, err := member.Do("POST", "/api/addons/apps/found-tool/"+verb, map[string]any{"operationId": nextID(verb), "generation": 1}, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(r.Status).To(Equal(403), n+" "+verb)
			}
		}
		// The administrator sees the controls on every node.
		for _, n := range nodes {
			raw, err := c.Session(n, "admin").Do("GET", "/api/addons/apps", nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(raw.String()).To(ContainSubstring(`"admin"`), n)
		}
	})

	It("returns the same typed denial from the owner and from each frontend", func() {
		var bodies []string
		for _, n := range nodes {
			r := read(c.Token(n, "friend"), "found-tool", res, 1, map[string]any{"tool": "echo"})
			Expect(r.Status).To(Equal(403))
			bodies = append(bodies, r.String())
		}
		Expect(bodies[1]).To(Equal(bodies[0]))
		Expect(bodies[2]).To(Equal(bodies[0]))
		var unknown []string
		for _, n := range nodes {
			r := read(c.Token(n, "member"), "found-tool", "r-not-a-resource", 1, map[string]any{"tool": "echo"})
			Expect(r.Status).To(Equal(403))
			unknown = append(unknown, r.String())
		}
		Expect(unknown[1]).To(Equal(unknown[0]))
		Expect(unknown[2]).To(Equal(unknown[0]))
		Expect(unknown[0]).To(Equal(bodies[0]), "a missing resource and a forbidden one look the same")
	})

	It("refuses a session on every node once it was ended through one frontend", func() {
		// A dedicated session: the shared fixture sessions stay valid.
		victim := c.Session(cluster.FrontA, "friend")
		for _, n := range nodes {
			status, _ := apps(c.Session(n, "friend"))
			Expect(status).To(Equal(200), n)
		}
		out, err := victim.Do("POST", "/api/auth/logout", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Status).To(Equal(200), out.String())
		for _, n := range nodes {
			status, _ := apps(c.Session(n, "friend"))
			Expect(status).To(Equal(401), n+": the ended session is refused everywhere")
			asset, err := c.Session(n, "friend").Do("GET", "/api/addons/found-tool/assets/"+"0000000000000000000000000000000000000000000000000000000000000000"+"/ui.js", nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(asset.Status).To(Equal(401), n)
		}
	})

	It("refuses an API key on every node once it was revoked through one frontend", func() {
		member := c.Creds.Users["member"]
		bearer := c.Token(cluster.FrontB, "member")
		Expect(actorOf(read(bearer, "found-tool", res, 1, map[string]any{"tool": "echo"}))).To(Equal("fnd-member"))
		r, err := c.Session(cluster.FrontA, "member").Do("DELETE", "/api/auth/api-keys/"+member.KeyID, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(200), r.String())
		for _, n := range nodes {
			got := read(c.Token(n, "member"), "found-tool", res, 1, map[string]any{"tool": "echo"})
			Expect(got.Status).To(Equal(401), n+": "+got.String())
		}
	})
})
