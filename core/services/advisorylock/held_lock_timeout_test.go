package advisorylock

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// hungConnector opens connections that accept session settings but never
// answer a query, like a database host that stopped responding mid-session.
type hungConnector struct{}

func (hungConnector) Connect(context.Context) (driver.Conn, error) { return hungConn{}, nil }
func (hungConnector) Driver() driver.Driver                        { return hungDriver{} }

type hungDriver struct{}

func (hungDriver) Open(string) (driver.Conn, error) { return hungConn{}, nil }

type hungConn struct{}

func (hungConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (hungConn) Close() error                        { return nil }
func (hungConn) Begin() (driver.Tx, error)           { return nil, errors.New("not supported") }
func (hungConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (hungConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

var _ = Describe("HeldLock against a hung database", func() {
	It("gives up an acquire after a bounded time, even with a context that never ends", func() {
		prev := heldLockCheckTimeout
		heldLockCheckTimeout = 200 * time.Millisecond
		DeferCleanup(func() { heldLockCheckTimeout = prev })

		db, err := gorm.Open(postgres.New(postgres.Config{Conn: sql.OpenDB(hungConnector{})}), &gorm.Config{DisableAutomaticPing: true})
		Expect(err).ToNot(HaveOccurred())
		l := NewHeldLock(db, 4242)

		done := make(chan error, 1)
		go func() {
			_, err := l.TryAcquire(context.Background())
			done <- err
		}()
		var got error
		Eventually(done, 5*time.Second).Should(Receive(&got))
		Expect(got).To(HaveOccurred())
		Expect(l.Held()).To(BeFalse())
	})
})
