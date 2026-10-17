package dialect_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/dialect"
)

// The CMS cannot see a host's DSN, so CheckSession asks the server how the
// connection behaves and names each required setting it finds missing.
// Each case starts from a correct DSN and breaks one setting.
func TestMySQLCheckSession(t *testing.T) {
	for _, engine := range dbtest.MySQLEngines {
		t.Run(string(engine), func(t *testing.T) {
			ctx := context.Background()
			check := func(params []string) error {
				db := dbtest.OpenMySQL(t, engine, params...)
				return dialect.MySQL{}.CheckSession(ctx, db.Execer())
			}
			// without returns the required params minus the one starting
			// with prefix, plus any replacements.
			without := func(prefix string, extra ...string) []string {
				params := slices.DeleteFunc(dbtest.RequiredMySQLParams(), func(p string) bool {
					return strings.HasPrefix(p, prefix)
				})
				return append(params, extra...)
			}

			if err := check(dbtest.RequiredMySQLParams()); err != nil {
				t.Fatalf("a correct DSN was refused: %v", err)
			}

			cases := []struct {
				name   string
				params []string
				want   string
			}{
				{"no parseTime", without("parseTime"), "parseTime=true"},
				{"a local loc", without("loc", "loc=America%2FHalifax"), "loc=UTC"},
				{"a zone at UTC only in winter", without("loc", "loc=Europe%2FLondon"), "loc=UTC"},
				{"a session off UTC", without("time_zone", "time_zone=%27%2B05%3A00%27"), "time_zone='+00:00'"},
				{"no clientFoundRows", without("clientFoundRows"), "clientFoundRows=true"},
			}
			for _, c := range cases {
				err := check(c.params)
				if err == nil {
					t.Errorf("%s: accepted, want an error naming %s", c.name, c.want)
					continue
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("%s: error %q does not name %s", c.name, err, c.want)
				}
				// It says how to fix it, too.
				if !strings.Contains(err.Error(), dialect.RequiredDSN) {
					t.Errorf("%s: error %q does not give the settings to add", c.name, err)
				}
			}

			// Everything missing at once is reported at once, not one per
			// restart.
			err := check([]string{"time_zone=%27-03%3A00%27"})
			for _, want := range []string{"parseTime=true", "time_zone='+00:00'", "clientFoundRows=true"} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("bare DSN: error %v does not name %s", err, want)
				}
			}
		})
	}
}
