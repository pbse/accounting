package transport

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// factText renders a confirmed input value the way the confirm form sent it.
func factText(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case []any:
		parts := []string{}
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(v)
	}
}

var amountText = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// sameFact compares amounts by value (1,200 = 1200.00) and everything else as text, so 0012 is not 12.
func sameFact(proposed, confirmed string) bool {
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), "")) }
	a, b := strings.ReplaceAll(norm(proposed), ",", ""), strings.ReplaceAll(norm(confirmed), ",", "")
	if amountText.MatchString(a) && amountText.MatchString(b) {
		x, _ := new(big.Rat).SetString(a)
		y, _ := new(big.Rat).SetString(b)
		return x.Cmp(y) == 0
	}
	return norm(proposed) == norm(confirmed)
}

// trackRecord compares the model's confirmed proposals with what people confirmed, and how their drafts were judged.
func trackRecord(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		from := time.Date(now.Year(), now.Month()-11, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
		scope := `c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted)) AND c.period>=$4`
		shown := `WITH clients AS (SELECT DISTINCT lower(c.entity) AS client FROM client_closes c WHERE ` + scope + ` ORDER BY 1 LIMIT 201), shown AS (SELECT client FROM clients ORDER BY client LIMIT 200) `
		var total, unlinked int
		err := db.QueryRowContext(r.Context(), shown+`SELECT (SELECT count(*) FROM clients),(SELECT count(*) FROM workpapers w JOIN client_closes c ON c.id=w.close_id JOIN shown s ON s.client=lower(c.entity) WHERE `+scope+` AND w.created_by IS NOT NULL)`, organization(r), firmAccess(r), subject(r), from).Scan(&total, &unlinked)
		if err != nil {
			problem(w, 503, "Track record is unavailable.")
			return
		}
		rows, err := db.QueryContext(r.Context(), shown+`SELECT c.entity,i.proposal,j.plans,
 (SELECT COALESCE(jsonb_agg(jsonb_build_object('decision',(SELECT v.decision FROM workpaper_reviews v WHERE v.workpaper_id=d.id ORDER BY v.id DESC LIMIT 1),'revised',EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=d.close_id AND n.paper->>'revises'=d.id::text))),'[]'::jsonb) FROM workpapers d WHERE d.close_id=c.id AND j.workpaper_ids @> jsonb_build_array(d.id::text))
 FROM client_closes c JOIN shown s ON s.client=lower(c.entity) JOIN preparation_jobs j ON j.close_id=c.id JOIN preparation_intakes i ON i.job_id=j.id
 WHERE `+scope+` AND i.confirmed_by IS NOT NULL AND i.proposal->>'engine'='openai' ORDER BY lower(c.entity),j.id`, organization(r), firmAccess(r), subject(r), from)
		if err != nil {
			problem(w, 503, "Track record is unavailable.")
			return
		}
		defer rows.Close()
		type client struct {
			Entity     string         `json:"entity"`
			Confirmed  int            `json:"proposals_confirmed"`
			Kept       int            `json:"proposals_kept"`
			Filled     int            `json:"filled_by_person"`
			Changed    map[string]int `json:"-"`
			Fields     []fieldCount   `json:"changed_fields"`
			Reviewed   int            `json:"drafts_reviewed"`
			Approved   int            `json:"drafts_approved_unrevised"`
			Revised    int            `json:"drafts_revised"`
			SentBack   int            `json:"drafts_sent_back"`
			normalized string
		}
		clients := []*client{}
		for rows.Next() {
			var entity string
			var rawProposal, rawPlans, rawDrafts []byte
			if err = rows.Scan(&entity, &rawProposal, &rawPlans, &rawDrafts); err != nil {
				break
			}
			if len(clients) == 0 || clients[len(clients)-1].normalized != strings.ToLower(entity) {
				clients = append(clients, &client{Entity: entity, Changed: map[string]int{}, normalized: strings.ToLower(entity)})
			}
			c := clients[len(clients)-1]
			var proposal struct {
				Kind  string `json:"kind"`
				Facts []struct {
					Field string `json:"field"`
					Value string `json:"value"`
				} `json:"facts"`
			}
			var plans []struct {
				Kind  string                     `json:"kind"`
				Input map[string]json.RawMessage `json:"input"`
			}
			if json.Unmarshal(rawProposal, &proposal) != nil || json.Unmarshal(rawPlans, &plans) != nil || len(plans) != 1 {
				continue
			}
			c.Confirmed++
			kept := proposal.Kind == plans[0].Kind
			if !kept {
				c.Changed["kind"]++
			}
			for _, f := range proposal.Facts {
				confirmed := factText(plans[0].Input[f.Field])
				switch {
				case strings.TrimSpace(f.Value) == "":
					if strings.TrimSpace(confirmed) != "" {
						c.Filled++
					}
				case !sameFact(f.Value, confirmed):
					kept = false
					c.Changed[f.Field]++
				}
			}
			if kept {
				c.Kept++
			}
			var drafts []struct {
				Decision *string `json:"decision"`
				Revised  bool    `json:"revised"`
			}
			json.Unmarshal(rawDrafts, &drafts)
			for _, d := range drafts {
				if !d.Revised && d.Decision == nil {
					continue
				}
				c.Reviewed++
				if d.Revised {
					c.Revised++
				}
				if d.Decision != nil && *d.Decision == "changes_requested" {
					c.SentBack++
				}
				if !d.Revised && d.Decision != nil && *d.Decision == "approved" {
					c.Approved++
				}
			}
		}
		if err == nil {
			err = rows.Err()
		}
		if err != nil {
			problem(w, 503, "Track record is unavailable.")
			return
		}
		for _, c := range clients {
			c.Fields = []fieldCount{}
			for _, field := range slices.Sorted(maps.Keys(c.Changed)) {
				c.Fields = append(c.Fields, fieldCount{field, c.Changed[field]})
			}
			slices.SortStableFunc(c.Fields, func(a, b fieldCount) int { return cmp.Compare(b.Count, a.Count) })
			c.Fields = c.Fields[:min(3, len(c.Fields))]
		}
		jsonReply(w, 200, map[string]any{"window_from": from, "clients": clients, "unlinked_human_drafts": unlinked, "has_more": total > 200})
	}
}

type fieldCount struct {
	Field string `json:"field"`
	Count int    `json:"count"`
}
