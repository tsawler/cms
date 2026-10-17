package dialect_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/dialect"
)

// The migration lock waits for as long as its context allows, as
// Postgres's does: past any single GET_LOCK round while another instance
// holds it, and no longer than ctx once the caller gives up. It used to
// give up after one 60-second wait, failing the startup of an instance
// that arrived during a long migration.
func TestMySQLLockWaitsForTheHolder(t *testing.T) {
	defer dialect.SetLockPoll(time.Second)()
	for _, engine := range dbtest.MySQLEngines {
		t.Run(string(engine), func(t *testing.T) {
			ctx := context.Background()
			holderDB := dbtest.OpenMySQL(t, engine, dbtest.RequiredMySQLParams()...)
			waiterDB := dbtest.OpenMySQL(t, engine, dbtest.RequiredMySQLParams()...)
			holder, err := holderDB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close()
			waiter, err := waiterDB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer waiter.Close()
			const key = "cms_test_lock"

			release, err := dialect.MySQL{}.Lock(ctx, holder.Execer(), key)
			if err != nil {
				t.Fatalf("first Lock: %v", err)
			}

			// Given up on: ctx ends while the lock is still held.
			short, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			_, err = dialect.MySQL{}.Lock(short, waiter.Execer(), key)
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Lock with the lock held past its context: err = %v, want DeadlineExceeded", err)
			}

			// Waited out: the holder lets go after several polling rounds,
			// and the waiter gets the lock. A connection of its own — the
			// driver abandons one whose query a cancelled context cut off,
			// which is what the case above just did.
			waiter2, err := waiterDB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer waiter2.Close()
			go func() {
				time.Sleep(2500 * time.Millisecond)
				release()
			}()
			started := time.Now()
			release2, err := dialect.MySQL{}.Lock(ctx, waiter2.Execer(), key)
			if err != nil {
				t.Fatalf("Lock while the holder finished: %v", err)
			}
			release2()
			if waited := time.Since(started); waited < 2*time.Second {
				t.Errorf("acquired after %v, before the holder let go", waited)
			}
		})
	}
}
