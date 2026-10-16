package cms

import (
	"context"
	"strings"
	"testing"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// Migrate is where a MySQL DSN missing a required setting is caught: at
// startup, by name, rather than later as sessions that expire hours early
// or saves that fail as "not found".
func TestMigrateRefusesAMisconfiguredMySQLDSN(t *testing.T) {
	for _, engine := range dbtest.MySQLEngines {
		t.Run(string(engine), func(t *testing.T) {
			ctx := context.Background()
			migrate := func(db *sqldb.DB) error {
				c := newTestCMS(t, db)
				defer c.Close()
				return c.Migrate(ctx)
			}

			if err := migrate(dbtest.OpenMySQL(t, engine, dbtest.RequiredMySQLParams()...)); err != nil {
				t.Fatalf("Migrate with a correct DSN: %v", err)
			}
			err := migrate(dbtest.OpenMySQL(t, engine, "parseTime=true", "loc=UTC", "time_zone=%27%2B00%3A00%27"))
			if err == nil || !strings.Contains(err.Error(), "clientFoundRows=true") {
				t.Errorf("Migrate without clientFoundRows = %v, want an error naming it", err)
			}
		})
	}
}
