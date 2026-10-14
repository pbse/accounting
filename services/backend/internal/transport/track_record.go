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

	"close.local/backend/internal/preparation"
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

type readProposal struct {
	Kind  string `json:"kind"`
	Facts []struct {
		Field string `json:"field"`
		Value string `json:"value"`
	} `json:"facts"`
}

type confirmedPlan struct {
	Kind  string                     `json:"kind"`
	Input map[string]json.RawMessage `json:"input"`
}

// compareProposal compares a model reading with what a person confirmed; expected fields the reading leaves blank are misses.
func compareProposal(p readProposal, plan confirmedPlan, expected []string) (bool, []string, int) {
	changed, filled, given := []string{}, 0, map[string]bool{}
	if p.Kind != preparation.WorkKind(plan.Kind, factText(plan.Input["schedule"])) {
		changed = append(changed, "kind")
	}
	for _, f := range p.Facts {
		if slices.Contains(preparation.IdentityFacts, f.Field) {
			continue
		}
		confirmed := factText(plan.Input[f.Field])
		switch {
		case strings.TrimSpace(f.Value) != "":
			given[f.Field] = true
			if !sameFact(f.Value, confirmed) {
				changed = append(changed, f.Field)
			}
		case strings.TrimSpace(confirmed) != "" && !slices.Contains(expected, f.Field):
			filled++
		}
	}
	for _, field := range expected {
		if !given[field] && !slices.Contains(changed, field) {
			changed = append(changed, field)
		}
	}
	return len(changed) == 0, changed, filled
}

// straightDraft is SQL: draft alias was prepared straight through by the agent, by its own flag or its job's agent
// confirmation, and complete: a reconciliation prepared with lines left for a person to classify is the agent's work,
// not straight through.
func straightDraft(alias string) string {
	return `((COALESCE((` + alias + `.paper->>'straight_through')::boolean,false) OR EXISTS(SELECT 1 FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.close_id=` + alias + `.close_id AND j.workpaper_ids @> jsonb_build_array(` + alias + `.id::text) AND i.confirmed_by='agent')) AND jsonb_array_length(COALESCE(NULLIF(` + alias + `.paper->'blockers','null'::jsonb),'[]'::jsonb))=0)`
}

// trackRecord compares the model's confirmed proposals with what people confirmed, and how their drafts were judged.
func trackRecord(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		from, to := time.Date(now.Year(), now.Month()-11, 1, 0, 0, 0, 0, time.UTC).Format("2006-01"), now.Format("2006-01")
		access := `c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted))`
		scope := access + ` AND c.period BETWEEN $4 AND $5`
		shown := `WITH clients AS (SELECT DISTINCT lower(c.entity) AS client FROM client_closes c WHERE ` + scope + ` ORDER BY 1 LIMIT 201), shown AS (SELECT client FROM clients ORDER BY client LIMIT 200) `
		var total, unlinked int
		err := db.QueryRowContext(r.Context(), shown+`SELECT (SELECT count(*) FROM clients),(SELECT count(*) FROM workpapers w JOIN client_closes c ON c.id=w.close_id JOIN shown s ON s.client=lower(c.entity) WHERE `+scope+` AND w.created_by IS NOT NULL)`, organization(r), firmAccess(r), subject(r), from, to).Scan(&total, &unlinked)
		if err != nil {
			problem(w, 503, "Track record is unavailable.")
			return
		}
		rows, err := db.QueryContext(r.Context(), shown+`SELECT c.entity,i.proposal,j.plans,
 (SELECT COALESCE(jsonb_agg(jsonb_build_object('decision',(SELECT v.decision FROM workpaper_reviews v WHERE v.workpaper_id=d.id ORDER BY v.id DESC LIMIT 1),'revised',EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=d.close_id AND n.paper->>'revises'=d.id::text))),'[]'::jsonb) FROM workpapers d WHERE d.close_id=c.id AND j.workpaper_ids @> jsonb_build_array(d.id::text))
 FROM client_closes c JOIN shown s ON s.client=lower(c.entity) JOIN preparation_jobs j ON j.close_id=c.id JOIN preparation_intakes i ON i.job_id=j.id
 WHERE `+scope+` AND i.confirmed_by IS NOT NULL AND i.confirmed_by<>'agent' AND i.proposal->>'engine' IN ('openai','context_machine') ORDER BY lower(c.entity),j.id`, organization(r), firmAccess(r), subject(r), from, to)
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
			var proposal readProposal
			var plans []confirmedPlan
			if json.Unmarshal(rawProposal, &proposal) != nil || json.Unmarshal(rawPlans, &plans) != nil || len(plans) != 1 {
				continue
			}
			c.Confirmed++
			kept, changed, filled := compareProposal(proposal, plans[0], nil)
			for _, field := range changed {
				c.Changed[field]++
			}
			c.Filled += filled
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
		// Where the window's work came from, each item counted once by its first draft. A revision the agent prepared
		// straight through (for a replaced source) is a refresh, counted apart; any other revision is a judgment.
		coverage := map[string]int{}
		names := []string{"straight_through", "person_confirmed", "carried", "other_agent", "people", "straight_judged", "straight_approved", "straight_revised", "straight_sent_back", "straight_refreshed", "failed_jobs", "unsupported_intakes"}
		values := make([]any, len(names))
		for i := range names {
			values[i] = new(int)
		}
		if err = db.QueryRowContext(r.Context(), `WITH d AS (SELECT w.created_by IS NOT NULL AS person,
 `+straightDraft("w")+` AS straight,
 EXISTS(SELECT 1 FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.close_id=w.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text) AND i.confirmed_by<>'agent') AS confirmed,
 w.paper ? 'carry_forward' OR w.paper ? 'recurring_accrual' AS carried,
 EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text AND NOT `+straightDraft("n")+`) AS revised,
 EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text AND `+straightDraft("n")+`) AS refreshed,
 (SELECT v.decision FROM workpaper_reviews v WHERE v.workpaper_id=w.id ORDER BY v.id DESC LIMIT 1) AS decision
 FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE `+scope+` AND COALESCE(w.paper->>'revises','')=''),
 a AS (SELECT *,NOT person AND straight AND NOT carried AS st FROM d)
 SELECT count(*) FILTER (WHERE st),count(*) FILTER (WHERE NOT person AND confirmed AND NOT straight AND NOT carried),count(*) FILTER (WHERE NOT person AND carried),
 count(*) FILTER (WHERE NOT person AND NOT straight AND NOT confirmed AND NOT carried),count(*) FILTER (WHERE person),
 count(*) FILTER (WHERE st AND (revised OR decision IS NOT NULL)),count(*) FILTER (WHERE st AND NOT revised AND decision='approved'),count(*) FILTER (WHERE st AND revised),count(*) FILTER (WHERE st AND decision='changes_requested'),count(*) FILTER (WHERE st AND refreshed),
 (SELECT count(*) FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id WHERE `+scope+` AND j.state='failed'),
 (SELECT count(*) FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN client_closes c ON c.id=j.close_id WHERE `+scope+` AND j.state<>'failed' AND i.proposal->>'kind'='unsupported')
 FROM a`, organization(r), firmAccess(r), subject(r), from, to).Scan(values...); err != nil {
			problem(w, 503, "Track record is unavailable.")
			return
		}
		for i, name := range names {
			coverage[name] = *values[i].(*int)
		}
		// Written-answer attempts on closes this member can open, over 30 days; a limit hit has no time.
		var asked, kept int
		var median *float64
		if err = db.QueryRowContext(r.Context(), `SELECT count(*),count(*) FILTER (WHERE a.outcome='kept'),percentile_cont(0.5) WITHIN GROUP (ORDER BY a.latency_ms) FILTER (WHERE a.latency_ms IS NOT NULL)/1000 FROM assistant_answers a JOIN client_closes c ON c.id=a.close_id WHERE a.organization_id=$1 AND `+access+` AND a.created_at>now()-interval '30 days'`, organization(r), firmAccess(r), subject(r)).Scan(&asked, &kept, &median); err != nil {
			problem(w, 503, "Track record is unavailable.")
			return
		}
		// Firm-wide attempts have no close; they hold no client details, so the firm's own count is shown to every member.
		var firmAsked, firmKept int
		if err = db.QueryRowContext(r.Context(), `SELECT count(*),count(*) FILTER (WHERE outcome='kept') FROM assistant_answers WHERE organization_id=$1 AND close_id IS NULL AND created_at>now()-interval '30 days'`, organization(r)).Scan(&firmAsked, &firmKept); err != nil {
			problem(w, 503, "Track record is unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"window_from": from, "clients": clients, "unlinked_human_drafts": unlinked, "window_to": to, "has_more": total > 200, "coverage": coverage, "assistant": map[string]any{"asked": asked, "kept": kept, "median_seconds": median, "firm_asked": firmAsked, "firm_kept": firmKept}})
	}
}

type fieldCount struct {
	Field string `json:"field"`
	Count int    `json:"count"`
}
