package transport

import (
	"crypto/rand"
	"database/sql"
	"os"
	"testing"
)

// The latest of a client's layouts is the newest, across a power of ten: ids 999999999 and 1000000000, sorted as text,
// gave the older (issue #369).
func TestTheLatestLayoutIsTheNewestAcrossAPowerOfTen(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	// Everything happens in a transaction rolled back at the end: nothing stays, and no other row is touched.
	tx, err := db.BeginTx(t.Context(), nil)
	must(err)
	defer tx.Rollback()
	org := "org_Order" + rand.Text()
	_, err = tx.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Order fixture')`, org)
	must(err)
	var file string
	must(tx.QueryRow(`INSERT INTO books_imports(organization_id,client_entity,period,name,digest,content,format,actor) VALUES($1,'Acme Co','2026-09','a.csv',md5(random()::text)||md5(random()::text),'x','csv','fixture') RETURNING id::text`, org).Scan(&file))
	// Two layouts either side of a power of ten, at ids of their own, far above any the shared sequence reaches.
	older, newer := "999999999", "1000000000"
	for _, id := range []string{older, newer} {
		_, err = tx.Exec(`INSERT INTO books_layouts(id,organization_id,client_entity,import_id,layout,reading,reason,actor,encoding,delimiter) OVERRIDING SYSTEM VALUE
 VALUES($3::bigint,$1,'Acme Co',$2::bigint,'{}','{}','Fixture','fixture','utf-8','comma')`, org, file, id)
		must(err)
	}
	for _, scope := range []string{file, ""} {
		if l, err := latestLayout(t.Context(), tx, org, "Acme Co", scope); err != nil || l == nil || l.ID != newer {
			t.Fatalf("the latest layout (%q): %v %v, want %s", scope, l, err, newer)
		}
	}
}
