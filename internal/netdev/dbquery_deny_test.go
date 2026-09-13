package netdev

import "testing"

// Audit P2: the SQL seal is exact-STATEMENT-PREFIX, so an allowlisted
// `select * from v_status` also admitted appended clauses like
// `… union select user,password from mysql.user` or MySQL
// `… into outfile '/path'` — neither carries the ';'/–-'/*' markers the
// single-statement checks catch. The hard-deny scan must refuse those while
// plain allowlisted SELECTs (with or without a WHERE/LIMIT tail) still pass.
func TestDBQueryAllowedDenyScan(t *testing.T) {
	allow := []string{"SELECT * FROM v_status"}
	ok := []string{
		"select * from v_status",
		"SELECT  * FROM v_status ;", // normalized to an exact match
		"select * from v_status where id = 1",
		"select * from v_status limit 5",
		"select * from v_status order by ts desc",
	}
	bad := []string{
		"select * from v_status union select user,password from mysql.user",
		"select * from v_status UNION ALL SELECT user FROM mysql.user",
		"select * from v_status into outfile '/var/tmp/x'",
		"select * from v_status into dumpfile '/var/tmp/x'",
		"select * from v_status where id = 1 for update",
		"select * from v_status lock in share mode",
	}
	for _, q := range ok {
		if !dbQueryAllowed(q, allow) {
			t.Errorf("%q should be allowed", q)
		}
	}
	for _, q := range bad {
		if dbQueryAllowed(q, allow) {
			t.Errorf("%q should be refused by the hard-deny scan", q)
		}
	}
}

// The same deny scan guards the Elasticsearch path seal: an entry-prefix
// match (`/_cat/indices`) must not admit a smuggled clause tail, including a
// %XX-encoded one in the query string (raw paths can't carry whitespace).
func TestESPathAllowedDenyScan(t *testing.T) {
	al := []string{"/_cat/indices"}
	if !esPathAllowed("/_cat/indices?v", al) {
		t.Fatal("/_cat/indices?v should be allowed")
	}
	if esPathAllowed("/_cat/indices?filter_path=union%20select", al) {
		t.Fatal("entry-prefix match must not admit an encoded UNION SELECT tail")
	}
}
