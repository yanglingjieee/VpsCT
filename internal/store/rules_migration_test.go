package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"ctlvps/internal/domain"
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
	// Of several rule sets one remains: with nobody using either, the first.
	rs, err := s.Rules(context.Background())
	want := "DOMAIN,panel.example.com,DIRECT\nRULE-SET,applications,DIRECT\nRULE-SET,cncidr,DIRECT\nRULE-SET,telegramcidr,PROXY,no-resolve\nMATCH,PROXY\n"
	if err != nil || rs.Rules != want || rs.GroupName != "🥔 土豆饼的家" || rs.UpdatedAt.IsZero() {
		t.Fatalf("%v: group %q, rules:\n%s", err, rs.GroupName, rs.Rules)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM rulesets`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("one set of rules remains: %v %d", err, rows)
	}
	var cols int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('rulesets') WHERE name IN ('mihomo','shadowrocket','surge','singbox','description','sort_order')`).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("the per-client and menu columns are gone: %v %d", err, cols)
	}
}

// With several rule sets, the one most users had becomes the panel's rules,
// and saving replaces it in place.
func TestOneRuleSetRemainsTheMostUsed(t *testing.T) {
	const before = 41
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
	for _, q := range []string{
		`DELETE FROM schema_version`,
		`INSERT INTO schema_version(version) VALUES (41)`,
		`INSERT INTO rulesets(id,name,rules,group_name,sort_order,created_at,updated_at) VALUES (1,'旧的','MATCH,DIRECT','旧组',0,'2026-10-06T00:00:00Z','2026-10-06T00:00:00Z')`,
		`INSERT INTO rulesets(id,name,rules,group_name,sort_order,created_at,updated_at) VALUES (2,'在用的','MATCH,PROXY','线路',1,'2026-10-06T00:00:00Z','2026-10-07T00:00:00Z')`,
		`INSERT INTO settings(key,value) VALUES ('rules.default_id','1')`,
		`INSERT INTO shares(name,ruleset_id,period_start,created_at,updated_at) VALUES ('a',2,'2026-10-01T00:00:00Z','2026-10-06T00:00:00Z','2026-10-06T00:00:00Z')`,
		`INSERT INTO shares(name,ruleset_id,period_start,created_at,updated_at) VALUES ('b',2,'2026-10-01T00:00:00Z','2026-10-06T00:00:00Z','2026-10-06T00:00:00Z')`,
		`INSERT INTO shares(name,ruleset_id,period_start,created_at,updated_at) VALUES ('c',1,'2026-10-01T00:00:00Z','2026-10-06T00:00:00Z','2026-10-06T00:00:00Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	rs, err := s.Rules(ctx)
	if err != nil || rs.Rules != "MATCH,PROXY" || rs.GroupName != "线路" {
		t.Fatalf("%v %+v", err, rs)
	}
	var named, setting int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM shares WHERE ruleset_id IS NOT NULL), (SELECT COUNT(*) FROM settings WHERE key='rules.default_id')`).Scan(&named, &setting); err != nil || named != 0 || setting != 0 {
		t.Fatalf("no user names a rule set and none is the default: %v %d %d", err, named, setting)
	}
	if shares, err := s.ListShares(ctx, nil); err != nil || len(shares) != 3 {
		t.Fatalf("users are kept: %v %d", err, len(shares))
	}
	next := rs
	next.Rules, next.GroupName = "DOMAIN,a.example,DIRECT\n", "新组"
	if err := s.SaveRules(ctx, &next); err != nil {
		t.Fatal(err)
	}
	var rows int
	if got, err := s.Rules(ctx); err != nil || got.Rules != next.Rules || got.GroupName != "新组" || s.db.QueryRow(`SELECT COUNT(*) FROM rulesets`).Scan(&rows) != nil || rows != 1 {
		t.Fatalf("saved in place: %v %+v rows=%d", err, got, rows)
	}
}

// A panel that never had rules saves its first.
func TestRulesStartEmpty(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "new.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if rs, err := s.Rules(ctx); err != nil || rs.Rules != "" || !rs.UpdatedAt.IsZero() {
		t.Fatalf("%v %+v", err, rs)
	}
	if err := s.SaveRules(ctx, &domain.Rules{Rules: "MATCH,PROXY\n", GroupName: "线路"}); err != nil {
		t.Fatal(err)
	}
	if rs, err := s.Rules(ctx); err != nil || rs.Rules != "MATCH,PROXY\n" || rs.GroupName != "线路" || rs.UpdatedAt.IsZero() {
		t.Fatalf("%v %+v", err, rs)
	}
}
