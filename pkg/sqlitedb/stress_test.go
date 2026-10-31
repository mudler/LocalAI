package sqlitedb

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type stressItem struct {
	ID      uint `gorm:"primaryKey"`
	Owner   int  `gorm:"index"`
	Counter int
	Body    string
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "sqlite_busy") || strings.Contains(m, "database is locked") || strings.Contains(m, "(5)") || strings.Contains(m, "(517)")
}

// The writers of one process (turn recorder, triggers, notifications, the
// conversion, the account store) all share one file. None of them may see
// SQLITE_BUSY: the busy timeout and the lock mode have to absorb it.
var _ = Describe("one SQLite file written by many goroutines", func() {
	It("never fails with SQLITE_BUSY or 'database is locked'", func() {
		db, err := Open(filepath.Join(GinkgoT().TempDir(), "stress.db"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			if sqlDB, e := db.DB(); e == nil {
				_ = sqlDB.Close()
			}
		})
		Expect(db.AutoMigrate(&stressItem{})).To(Succeed())
		Expect(db.Create(&stressItem{ID: 1}).Error).To(Succeed())

		const workers, rounds = 32, 40
		// A goroutine that keeps a cursor open and writes needs a second connection,
		// so the holders stay fewer than the pool (the rule is in the storage docs).
		holders := make(chan struct{}, 4)
		var busy, other atomic.Int64
		var first atomic.Value
		note := func(err error) {
			if err == nil {
				return
			}
			if isBusy(err) {
				busy.Add(1)
			} else {
				other.Add(1)
			}
			first.CompareAndSwap(nil, err.Error())
		}

		var wg sync.WaitGroup
		for w := range workers {
			wg.Add(1)
			go func(w int) {
				defer GinkgoRecover()
				defer wg.Done()
				for r := range rounds {
					switch (w + r) % 5 {
					case 0: // single insert (implicit transaction)
						note(db.Create(&stressItem{Owner: w, Body: strings.Repeat("x", 200)}).Error)
					case 1: // autocommit update, outside any transaction
						note(db.Exec("UPDATE stress_items SET counter = counter + 1 WHERE id = 1").Error)
					case 2: // read then write in one transaction
						note(db.Transaction(func(tx *gorm.DB) error {
							var it stressItem
							if err := tx.First(&it, 1).Error; err != nil {
								return err
							}
							return tx.Model(&stressItem{}).Where("id = 1").Update("counter", it.Counter+1).Error
						}))
					case 3: // a read cursor held open while the same goroutine writes
						holders <- struct{}{}
						rows, err := db.Model(&stressItem{}).Where("owner >= 0").Rows()
						if err != nil {
							note(err)
							<-holders
							break
						}
						for rows.Next() {
							note(db.Create(&stressItem{Owner: w}).Error)
							break
						}
						note(rows.Close())
						<-holders
					case 4: // plain list
						var l []stressItem
						note(db.Limit(50).Find(&l).Error)
					}
				}
			}(w)
		}
		wg.Wait()
		msg, _ := first.Load().(string)
		Expect(busy.Load()+other.Load()).To(BeZero(), fmt.Sprintf("busy=%d other=%d first=%s", busy.Load(), other.Load(), msg))
	})
})
