// SPDX-License-Identifier: MIT
package packaging

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This audit records calls, including the mutation/undo wrappers' operation
// arguments. It is intentionally independent of deployment prefix grants.
var officialRequests = map[string]contract.HostOperations{
	"fine-tuning":  {Required: strings.Fields("finetune.checkpoints finetune.dataset finetune.export finetune.get finetune.list finetune.start finetune.stop")},
	"quantization": {Required: strings.Fields("quantize.get quantize.import quantize.list quantize.start quantize.stop")},
	"office":       {Required: strings.Fields("workspace.export workspace.import")},
	"sandbox":      {Required: strings.Fields("workspace.changes workspace.commit workspace.materialize"), Optional: strings.Fields("egress.audit egress.check workspace.admin")},
	"web":          {Optional: []string{"workspace.admin"}},
	// A connector moves files only when the person turns files on, so both
	// file operations are optional. Delivery is pull, so neither add-on calls
	// connector.inbound.
	"slack":    {Optional: strings.Fields("connector.file.get connector.file.put")},
	"telegram": {Optional: strings.Fields("connector.file.get connector.file.put")},
}

// connectorRoot holds the file transfer code that slack and telegram share. It
// names its operations by constant, so the audit reads those too.
const connectorRoot = "addons/internal/connector"

var connectorConstants = map[string]string{
	"ConnectorInboundOp": contract.ConnectorInboundOp, "ConnectorFilePutOp": contract.ConnectorFilePutOp, "ConnectorFileGetOp": contract.ConnectorFileGetOp,
}

var _ = Describe("official typed package contracts", func() {
	for _, id := range firstPartyAddons {
		It("audits actual host calls and strict package contract for "+id, func() {
			root := filepath.Join("../../..", "addons", id)
			data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
			Expect(err).NotTo(HaveOccurred())
			m, err := contract.ParseManifest(data)
			Expect(err).NotTo(HaveOccurred())
			want, requests := officialRequests[id]
			if requests {
				Expect(m.Capabilities.HostOperations).To(Equal(&want))
			} else {
				Expect(m.Capabilities.HostOperations).To(BeNil(), "no hostclient calls: do not manufacture requests from policy grants")
			}
			// Collect exact operation literals in production source, including fields
			// passed to jobundo. An unknown operation-shaped literal is not evidence
			// of support; compare independently with the real registered vocabulary.
			var used []string
			roots := []string{root}
			if id == "slack" || id == "telegram" {
				roots = append(roots, filepath.Join("../../..", connectorRoot))
			}
			for _, root := range roots {
				Expect(filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
						return nil
					}
					f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
					if err != nil {
						return err
					}
					ast.Inspect(f, func(n ast.Node) bool {
						if sel, ok := n.(*ast.SelectorExpr); ok {
							if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "contract" {
								if value, ok := connectorConstants[sel.Sel.Name]; ok {
									used = append(used, value)
								}
							}
						}
						lit, ok := n.(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							return true
						}
						value, err := strconv.Unquote(lit.Value)
						Expect(err).NotTo(HaveOccurred())
						if slices.Contains(hostops.SupportedOperations(), value) {
							used = append(used, value)
						}
						return true
					})
					return nil
				})).To(Succeed())
			}
			declared := append(slices.Clone(want.Required), want.Optional...)
			slices.Sort(used)
			used = slices.Compact(used)
			Expect(used).To(ConsistOf(declared), "update the audited call inventory when production calls change")
			for _, name := range declared {
				Expect(hostops.SupportedOperations()).To(ContainElement(name))
			}
			policyPath, err := filepath.Abs("../../../deploy/addons/host-policy.json")
			Expect(err).NotTo(HaveOccurred())
			grants, err := config.LoadAddonHostPolicy(policyPath)
			Expect(err).NotTo(HaveOccurred())
			summary := hostops.Requests(m.Capabilities.HostOperations, hostops.SupportedOperations(), grants[id])
			Expect(summary.MissingRequired).To(BeEmpty())
			denied := hostops.Requests(m.Capabilities.HostOperations, hostops.SupportedOperations(), nil)
			Expect(denied.Granted).To(BeEmpty(), "requests never self-grant")
			Expect(denied.MissingRequired).To(ConsistOf(want.Required))
			p := newProjectFor(id, map[string][]string{amd: nil})
			layout := p.mustBuild(amd, "1.2.3", "fixture executable")
			Expect(p.verify(layout, amd, "1.2.3")).To(Succeed())
		})
	}
})
