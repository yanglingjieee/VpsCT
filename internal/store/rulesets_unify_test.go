package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// A rule set written as one profile per client comes out of the upgrade as
// the one list of rules, with the name of its selector kept: clients remember
// the chosen line under that name.
func TestRulesetsBecomeOneList(t *testing.T) {
	const before = 38
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:before] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatal(err)
		}
	}
	clash := "proxy-groups:\n  - {name: 🥔 土豆饼的家, type: select, proxies: ['{{all}}']}\nrules:\n  - DOMAIN,panel.example.com,DIRECT\n  - RULE-SET,applications,DIRECT\n  - RULE-SET,cncidr,DIRECT\n  - RULE-SET,telegramcidr,🥔 土豆饼的家,no-resolve\n  - MATCH,🥔 土豆饼的家\n"
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM schema_version`, nil},
		{`INSERT INTO schema_version(version) VALUES (?)`, []any{before}},
		{`INSERT INTO rulesets(id,name,mihomo,shadowrocket,created_at,updated_at) VALUES (1,'土豆饼规则',?,'[Rule]','2026-10-06T00:00:00Z','2026-10-06T00:00:00Z')`, []any{clash}},
		{`INSERT INTO rulesets(id,name,shadowrocket,created_at,updated_at) VALUES (2,'只有小火箭','[Rule]','2026-10-06T00:00:00Z','2026-10-06T00:00:00Z')`, nil},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rs, err := s.GetRuleset(context.Background(), 1)
	want := "DOMAIN,panel.example.com,DIRECT\nRULE-SET,applications,DIRECT\nRULE-SET,cncidr,DIRECT\nRULE-SET,telegramcidr,PROXY,no-resolve\nMATCH,PROXY\n"
	if err != nil || rs.Rules != want || rs.GroupName != "🥔 土豆饼的家" || rs.Name != "土豆饼规则" {
		t.Fatalf("%v: group %q, rules:\n%s", err, rs.GroupName, rs.Rules)
	}
	if rs, err = s.GetRuleset(context.Background(), 2); err != nil || rs.Rules != "" || rs.GroupName != "" {
		t.Fatalf("a rule set without a Clash profile starts from the default: %v %+v", err, rs)
	}
	var cols int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('rulesets') WHERE name IN ('mihomo','shadowrocket','surge','singbox')`).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("the per-client columns are gone: %v %d", err, cols)
	}
}
