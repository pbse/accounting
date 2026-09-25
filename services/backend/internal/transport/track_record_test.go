package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestTrackRecordComparesProposalsWithWhatPeopleConfirmed(t *testing.T) {
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
	org, other := "org_Track"+rand.Text(), "org_Track"+rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Track fixture'),($2,'Other firm')`, org, other)
	must(err)
	for _, subject := range []string{"user_partner", "user_member"} {
		_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,'Synthetic enrollment','fixture')`, org, subject)
		must(err)
	}
	month := time.Now().UTC().Format("2006-01")
	var harbor, foreign, old, edge string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Harbor Dental',$2) RETURNING id::text`, org, month).Scan(&harbor))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Harbor Dental',$2) RETURNING id::text`, org, time.Now().UTC().AddDate(-2, 0, 0).Format("2006-01")).Scan(&old))
	now := time.Now().UTC()
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Harbor Dental',$2) RETURNING id::text`, org, time.Date(now.Year(), now.Month()-12, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")).Scan(&edge))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Foreign Bakery',$2) RETURNING id::text`, other, month).Scan(&foreign))
	defer func() {
		for _, id := range []string{harbor, foreign, old, edge} {
			db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, id)
			db.Exec(`UPDATE evidence SET superseded_by=NULL WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id IN ($1,$2)`, org, other)
	}()
	document := func(closeID string) (id, digest string) {
		t.Helper()
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,'Invoice','fixture') RETURNING id::text,digest`, closeID, rand.Text()).Scan(&id, &digest))
		return id, digest
	}
	paper := func(closeID, body string, by any) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper,created_by) VALUES($1,'prepaid',$2,$3,$4) RETURNING id::text`, closeID, rand.Text(), body, by).Scan(&id))
		return id
	}
	review := func(paperID, decision string) {
		t.Helper()
		_, e := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) SELECT id,digest,$2,'Reviewed','user_partner' FROM workpapers WHERE id=$1`, paperID, decision)
		must(e)
	}
	intake := func(closeID, engine, facts, input string, confirmed bool, drafts ...string) {
		t.Helper()
		source, _ := document(closeID)
		ids, _ := json.Marshal(drafts)
		var job string
		must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by,workpaper_ids) VALUES($1,$2,$3,'completed','user_partner',$4) RETURNING id::text`, closeID, rand.Text(), `[{"kind":"prepaid","input":`+input+`,"sources":[]}]`, string(ids)).Scan(&job))
		var by any
		if confirmed {
			by = "user_partner"
		}
		_, e := db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal,confirmed_by,confirmed_at) VALUES($1,$2,$3,$4,now())`, job, source, `{"kind":"prepaid","engine":"`+engine+`","facts":`+facts+`}`, by)
		must(e)
	}

	replaced, replacedDigest := document(harbor)
	kept := paper(harbor, `{"input":{"source_reference":"Allianz"},"sources":[{"id":"`+replaced+`","digest":"`+replacedDigest+`"}]}`, nil)
	review(kept, "approved")
	newer, _ := document(harbor)
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$2 WHERE id=$1`, replaced, newer)
	must(err)
	intake(harbor, "openai", `[{"field":"prepaid_account","value":"Prepaid insurance"},{"field":"amount","value":"1,200"}]`, `{"prepaid_account":"prepaid  insurance","amount":"1200.00"}`, true, kept)
	_, err = db.Exec(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,$3,'queued','user_partner')`, harbor, rand.Text(), `[{"kind":"prepaid","revises":"`+kept+`","input":{},"sources":[]}]`)
	must(err)

	corrected := paper(harbor, `{"input":{"source_reference":"Hiscox"}}`, nil)
	review(corrected, "changes_requested")
	paper(harbor, `{"revises":"`+corrected+`","input":{"source_reference":"Hiscox"}}`, nil)
	intake(harbor, "openai", `[{"field":"prepaid_account","value":"Prepaid rent"},{"field":"amount","value":"800"},{"field":"booking_reference","value":"0012"}]`, `{"prepaid_account":"Prepaid insurance","amount":"800","booking_reference":"12"}`, true, corrected)

	sentBack := paper(harbor, `{"input":{"source_reference":"Lease"}}`, nil)
	review(sentBack, "changes_requested")
	intake(harbor, "openai", `[{"field":"service_end","value":""},{"field":"amount","value":"600"}]`, `{"service_end":"2026-12-31","amount":"600"}`, true, sentBack)

	intake(harbor, "openai", `[{"field":"amount","value":"50"}]`, `{"amount":"70"}`, false, paper(harbor, `{"input":{"source_reference":"Unconfirmed"}}`, nil))
	intake(harbor, "local", `[{"field":"amount","value":"90"}]`, `{"amount":"95"}`, true, paper(harbor, `{"input":{"source_reference":"Rules"}}`, nil))
	_, err = db.Exec(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[{"kind":"prepaid","carry_forward":{},"input":{},"sources":[]}]','completed','user_partner')`, harbor, rand.Text())
	must(err)
	paper(harbor, `{"input":{"source_reference":"Typed by hand"}}`, "user_partner")
	intake(old, "openai", `[{"field":"amount","value":"20"}]`, `{"amount":"25"}`, true)
	intake(edge, "openai", `[{"field":"amount","value":"20"}]`, `{"amount":"25"}`, true)
	intake(harbor, "openai", `[{"field":"amount","value":"300"}]`, `{"amount":"300"}`, true)
	_, err = db.Exec(`UPDATE preparation_intakes SET proposal=jsonb_set(proposal,'{kind}','"accrual"') WHERE job_id=(SELECT max(id) FROM preparation_jobs WHERE close_id=$1)`, harbor)
	must(err)
	intake(foreign, "openai", `[{"field":"amount","value":"10"}]`, `{"amount":"10"}`, true, paper(foreign, `{"input":{"source_reference":"Foreign"}}`, nil))

	mux := http.NewServeMux()
	registerEffort(mux, db)
	h := organizationBoundary(db, mux)
	type client struct {
		Entity    string
		Confirmed int `json:"proposals_confirmed"`
		Kept      int `json:"proposals_kept"`
		Filled    int `json:"filled_by_person"`
		Fields    []struct {
			Field string
			Count int
		} `json:"changed_fields"`
		Reviewed int `json:"drafts_reviewed"`
		Approved int `json:"drafts_approved_unrevised"`
		Revised  int `json:"drafts_revised"`
		SentBack int `json:"drafts_sent_back"`
	}
	read := func(permissions ...string) (out struct {
		Clients  []client
		Unlinked int    `json:"unlinked_human_drafts"`
		Window   string `json:"window_from"`
	}) {
		t.Helper()
		subject := "user_member"
		if len(permissions) > 0 {
			subject = "user_partner"
		}
		actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}
		r := httptest.NewRequest("GET", "/v1/closes/track-record", nil).WithContext(auth.WithActor(t.Context(), actor))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("track record: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	got := read("clients:all")
	if len(got.Clients) != 1 || got.Clients[0].Entity != "Harbor Dental" || got.Window == "" {
		t.Fatalf("only this firm's clients with confirmed model proposals are listed, with the window: %+v", got)
	}
	c := got.Clients[0]
	if c.Confirmed != 4 || c.Kept != 2 || c.Filled != 1 || len(c.Fields) != 3 || c.Fields[0].Field != "booking_reference" || c.Fields[1].Field != "kind" || c.Fields[2].Field != "prepaid_account" {
		t.Errorf("a changed value or kind is a change, a filled blank is not, and older closes are outside the window: %+v", c)
	}
	if c.Reviewed != 3 || c.Approved != 1 || c.Revised != 1 || c.SentBack != 2 {
		t.Errorf("drafts are judged by recorded decisions and actual revisions, and a sent-back draft that was revised counts as both: %+v", c)
	}
	if got.Unlinked != 1 {
		t.Errorf("drafts people saved directly are counted as not covered: %d", got.Unlinked)
	}
	if member := read(); len(member.Clients) != 0 {
		t.Fatalf("a member without the client grant sees nothing: %+v", member.Clients)
	}
}
