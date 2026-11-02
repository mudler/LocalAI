// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var _ = Describe("folder admission before acceptance", func() {
	for _, backend := range []string{"JSON", "SQLite", "PostgreSQL"} {
		for _, delivery := range []string{"direct", "request", "attachment"} {
			backend, delivery := backend, delivery
			It("leaves no acceptance on refusal and retries once on "+backend+" "+delivery, func() {
				ctx := context.Background()
				original, ws, ag := capacityService()
				path := filepath.Join(GinkgoT().TempDir(), "chats")
				var st store.Store
				var err error
				if backend == "JSON" {
					st, err = store.NewJSON(path)
				} else {
					dsn := path
					if backend == "PostgreSQL" {
						raw := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
						Expect(raw).NotTo(BeEmpty(), "PostgreSQL fixture required")
						admin, e := gorm.Open(postgres.Open(raw), &gorm.Config{})
						Expect(e).NotTo(HaveOccurred())
						schema := fmt.Sprintf("capacity_acceptance_%d", time.Now().UnixNano())
						Expect(admin.Exec("CREATE SCHEMA " + schema).Error).To(Succeed())
						DeferCleanup(func() {
							Expect(admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error).To(Succeed())
							db, _ := admin.DB()
							_ = db.Close()
						})
						u, e := url.Parse(raw)
						Expect(e).NotTo(HaveOccurred())
						q := u.Query()
						q.Set("search_path", schema)
						u.RawQuery = q.Encode()
						dsn = u.String()
					}
					var db *gorm.DB
					if backend == "SQLite" {
						db, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
					} else {
						db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
					}
					Expect(err).NotTo(HaveOccurred())
					sqlDB, e := db.DB()
					Expect(e).NotTo(HaveOccurred())
					DeferCleanup(func() { _ = sqlDB.Close() })
					st, err = store.NewGORM(db)
				}
				Expect(err).NotTo(HaveOccurred())
				ag.CollectionPermissions = &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "notes"}}}
				ag.Memory = &agents.MemorySettings{Version: 1, Destination: "notes", ToolStorage: "content", AutomaticIngestion: "incoming_and_replies"}
				Expect(original.d.Agents.Put(ctx, ag)).To(Succeed())
				deps := original.d
				deps.Store = st
				deps.StateDir = GinkgoT().TempDir()
				deps.Harness = studiotest.NewGateHarness()
				svc, err := New(deps)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { _ = svc.Close(ctx) })
				rec, err := svc.Create(ctx, "alice", CreateInput{AgentID: ag.ID})
				Expect(err).NotTo(HaveOccurred())
				roots, err := svc.readOnlyRoots(ctx, userChatActor("alice", rec.ID))
				Expect(err).NotTo(HaveOccurred())
				var extra workspace.Node
				for i := 0; i < workspace.MaxFenceRoots-1-len(roots); i++ {
					extra = capacityShare(ws, i)
				}
				h := deps.Harness.(*studiotest.GateHarness)
				send := func() (SendResult, error) {
					sendCtx := ctx
					if delivery != "direct" {
						sendCtx = withMemoryDelivery(ctx, "capacity-request")
					}
					var up []session.Upload
					if delivery == "attachment" {
						up = []session.Upload{{Name: "note.txt", Body: strings.NewReader("attachment")}}
					}
					return svc.SendWithFiles(sendCtx, "alice", rec.ID, "remember", up)
				}
				_, err = send()
				Expect(err).To(MatchError(ErrAgentFolderLimit))
				pending, err := st.Pending(ctx, "alice", rec.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(pending).To(BeEmpty())
				_, found, err := st.MemoryInput(ctx, "alice", rec.ID, "memory:capacity-request")
				Expect(err).NotTo(HaveOccurred())
				Expect(found).To(BeFalse())
				captures, err := st.ListCaptures(ctx, "alice", rec.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(captures).To(BeEmpty())
				Expect(h.Starts()).To(BeZero())
				Expect(h.Sent()).To(BeEmpty())
				turn, err := st.Turn(ctx, "alice", rec.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(turn.Running).To(BeFalse())
				Expect(ws.Revoke(ctx, workspace.Actor{Principal: files.AdminPrincipal}, files.EveryoneGroup, extra.Path)).To(Succeed())
				result, err := send()
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Started).To(BeTrue())
				Eventually(h.Sent).Should(HaveLen(1))
				h.Gate <- struct{}{}
				Eventually(func() store.TurnStatus { t, _ := st.Turn(ctx, "alice", rec.ID); return t.Last }).Should(Equal(store.TurnDone))
				if delivery != "direct" {
					Expect(ws.Grant(ctx, workspace.Actor{Principal: files.AdminPrincipal}, files.EveryoneGroup, extra.Path, workspace.ModeWrite)).To(Succeed())
					replay, err := send()
					Expect(err).NotTo(HaveOccurred())
					Expect(replay).To(Equal(result))
					Expect(h.Sent()).To(HaveLen(1))
				}
			})
		}
	}
})
