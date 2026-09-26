package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// paperChain is a draft ($1, in close $2) and every draft it revises, nearest first, to the end of the chain.
const paperChain = `WITH RECURSIVE chain(id,paper,depth) AS (SELECT id,paper,0 FROM workpapers WHERE id::text=$1 AND close_id::text=$2
 UNION ALL SELECT p.id,p.paper,c.depth+1 FROM chain c JOIN workpapers p ON p.id::text=c.paper->>'revises' AND p.close_id::text=$2) `

var commentAnchor = regexp.MustCompile(`^(general|field:([a-z0-9_]{1,60})|source:([0-9]+):line:([0-9]+))$`)

// openComments counts unresolved comments on a draft and its predecessors.
func openComments(ctx context.Context, tx *sql.Tx, closeID, paperID string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, paperChain+`SELECT count(*) FROM workpaper_comments WHERE workpaper_id IN (SELECT id FROM chain) AND resolved_at IS NULL`, paperID, closeID).Scan(&n)
	return n, err
}

type canvasComment struct {
	ID          string     `json:"id"`
	WorkpaperID string     `json:"workpaper_id"`
	Anchor      string     `json:"anchor"`
	AnchorLabel string     `json:"anchor_label"`
	Body        string     `json:"body"`
	Author      string     `json:"author"`
	CreatedAt   time.Time  `json:"created_at"`
	ResolvedBy  string     `json:"resolved_by,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	Resolution  string     `json:"resolution,omitempty"`
}

func registerCanvas(mux *http.ServeMux, db *sql.DB) {
	base := "/v1/closes/{id}/workpapers/{paper}"
	comments := func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(), paperChain+`SELECT m.id::text,m.workpaper_id::text,m.anchor,m.anchor_label,m.body,m.author,m.created_at,COALESCE(m.resolved_by,''),m.resolved_at,COALESCE(m.resolution,'') FROM workpaper_comments m JOIN chain c ON c.id=m.workpaper_id ORDER BY m.resolved_at IS NOT NULL,m.id`, r.PathValue("paper"), r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Comments are unavailable.")
			return
		}
		defer rows.Close()
		out := []canvasComment{}
		for rows.Next() {
			var c canvasComment
			if err = rows.Scan(&c.ID, &c.WorkpaperID, &c.Anchor, &c.AnchorLabel, &c.Body, &c.Author, &c.CreatedAt, &c.ResolvedBy, &c.ResolvedAt, &c.Resolution); err != nil {
				problem(w, 503, "Comments are unavailable.")
				return
			}
			out = append(out, c)
		}
		if rows.Err() != nil {
			problem(w, 503, "Comments are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"comments": out})
	}
	mux.HandleFunc("GET "+base+"/comments", comments)
	mux.HandleFunc("POST "+base+"/comments", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action     string `json:"action"`
			ID         string `json:"id"`
			Anchor     string `json:"anchor"`
			Body       string `json:"body"`
			Resolution string `json:"resolution"`
		}
		if !decode(w, r, &in) {
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Comments are unavailable.")
			return
		}
		defer tx.Rollback()
		// The close's lock orders a comment against an approval, which counts open comments under the same lock.
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		paperID, me := r.PathValue("paper"), subject(r)
		switch in.Action {
		case "resolve":
			in.Resolution = strings.TrimSpace(in.Resolution)
			if n := utf8.RuneCountInString(in.Resolution); n < 1 || n > 500 {
				problem(w, 422, "Say how it was resolved, in 1–500 characters.")
				return
			}
			result, err := tx.ExecContext(r.Context(), paperChain+`UPDATE workpaper_comments SET resolved_by=$4,resolved_at=now(),resolution=$5 WHERE id::text=$3 AND resolved_at IS NULL AND workpaper_id IN (SELECT id FROM chain)`, paperID, closeID, in.ID, me, in.Resolution)
			if n, _ := result.RowsAffected(); err != nil || n != 1 {
				problem(w, 409, "That comment is already resolved or not on this draft.")
				return
			}
		case "add":
			var raw []byte
			var state string
			var current bool
			if err = tx.QueryRowContext(r.Context(), `SELECT w.paper,w.review_state,NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text) FROM workpaper_review_state w WHERE w.id::text=$1 AND w.close_id::text=$2`, paperID, closeID).Scan(&raw, &state, &current); err != nil {
				problem(w, 404, "Workpaper not found.")
				return
			}
			if !current || state == "approved" {
				problem(w, 409, "Comment on the current, unapproved draft. To question an approved draft, request changes first.")
				return
			}
			in.Body = strings.TrimSpace(in.Body)
			if n := utf8.RuneCountInString(in.Body); n < 1 || n > 2000 {
				problem(w, 422, "Write the comment in 1–2,000 characters.")
				return
			}
			label, ok := anchorLabel(r.Context(), tx, closeID, in.Anchor, raw)
			if !ok {
				problem(w, 422, "Anchor the comment to the whole draft, one of its inputs, or a line of one of its sources.")
				return
			}
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO workpaper_comments(workpaper_id,anchor,anchor_label,body,author) VALUES($1,$2,$3,$4,$5)`, paperID, in.Anchor, label, in.Body, me); err != nil {
				problem(w, 503, "The comment could not be saved.")
				return
			}
		default:
			problem(w, 422, "Add or resolve a comment.")
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "The comment could not be saved.")
			return
		}
		comments(w, r)
	})
	mux.HandleFunc("GET "+base+"/since-review", func(w http.ResponseWriter, r *http.Request) {
		paperID, closeID, me := r.PathValue("paper"), r.PathValue("id"), reviewerName(r)
		var current []byte
		var state, latestDecision, latestNote, latestActor string
		var latestID int64
		var latestAt sql.NullTime
		var newer sql.NullString
		err := db.QueryRowContext(r.Context(), `SELECT w.paper,w.review_state,COALESCE(r.decision,''),COALESCE(r.note,''),COALESCE(r.actor,''),COALESCE(r.id,0),r.created_at,(SELECT n.id::text FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text LIMIT 1)
 FROM workpaper_review_state w LEFT JOIN LATERAL (SELECT * FROM workpaper_reviews x WHERE x.workpaper_id=w.id ORDER BY x.id DESC LIMIT 1) r ON true WHERE w.id::text=$1 AND w.close_id::text=$2`, paperID, closeID).Scan(&current, &state, &latestDecision, &latestNote, &latestActor, &latestID, &latestAt, &newer)
		if err == sql.ErrNoRows {
			problem(w, 404, "Workpaper not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Review history unavailable.")
			return
		}
		out := map[string]any{"last_review": nil, "changes": []paperChange{}, "reasons": []string{}}
		reasons := []string{}
		if newer.Valid {
			reasons = append(reasons, "A newer draft #"+newer.String+" replaces this one.")
		}
		var reviewedID, decision, note string
		var reviewID int64
		var at time.Time
		var reviewed []byte
		err = db.QueryRowContext(r.Context(), paperChain+`SELECT c.id::text,c.paper,x.id,x.decision,x.note,x.created_at FROM workpaper_reviews x JOIN chain c ON c.id=x.workpaper_id WHERE x.actor=$3 ORDER BY x.id DESC LIMIT 1`, paperID, closeID, me).Scan(&reviewedID, &reviewed, &reviewID, &decision, &note, &at)
		switch {
		case err == nil:
			out["last_review"] = map[string]any{"workpaper_id": reviewedID, "decision": decision, "note": note, "at": at}
			if reviewedID != paperID {
				out["changes"] = paperChanges(reviewed, current)
				revisions, e := revisedSince(r.Context(), db, closeID, paperID, reviewedID)
				if e != nil {
					problem(w, 503, "Review history unavailable.")
					return
				}
				reasons = append(reasons, "Revised since your review: draft #"+strings.Join(append(revisions, paperID), " → #")+".")
			}
		case err != sql.ErrNoRows:
			problem(w, 503, "Review history unavailable.")
			return
		}
		sources, parent, e := reopenedBy(r.Context(), db, closeID, current)
		if e != nil {
			problem(w, 503, "Review history unavailable.")
			return
		}
		reasons = append(append(reasons, sources...), parent...)
		// A changes-requested decision explains the state only while it is the draft's latest and newer than your review.
		if state == "changes_requested" && latestDecision == "changes_requested" && latestID > reviewID {
			reasons = append(reasons, fmt.Sprintf("%s requested changes on %s: %s", latestActor, latestAt.Time.Format("2 Jan 2006"), latestNote))
		}
		out["reasons"] = reasons
		out["state"] = state
		jsonReply(w, 200, out)
	})
}

// revisedSince lists the drafts between the one you reviewed and the current one, oldest first.
func revisedSince(ctx context.Context, db *sql.DB, closeID, paperID, reviewedID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, paperChain+`SELECT id::text FROM chain WHERE depth>0 AND depth<=(SELECT depth FROM chain WHERE id::text=$3) ORDER BY depth DESC`, paperID, closeID, reviewedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type lineageLink struct {
	PaperID  string `json:"paper_id"`
	Digest   string `json:"digest"`
	ReviewID string `json:"review_id"`
}

// reopenedBy names the current draft's linked sources that are missing, changed or replaced, and an inherited schedule
// that is no longer the approved version the draft was built from.
func reopenedBy(ctx context.Context, db *sql.DB, closeID string, raw []byte) ([]string, []string, error) {
	var paper struct {
		Sources []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"sources"`
		CarryForward     *lineageLink `json:"carry_forward"`
		RecurringAccrual *lineageLink `json:"recurring_accrual"`
	}
	json.Unmarshal(raw, &paper)
	sources := []string{}
	for _, s := range paper.Sources {
		var digest, reason string
		var replacement sql.NullString
		err := db.QueryRowContext(ctx, `SELECT digest,superseded_by::text,COALESCE(replacement_reason,'') FROM evidence WHERE id::text=$1 AND close_id::text=$2`, s.ID, closeID).Scan(&digest, &replacement, &reason)
		switch {
		case err == sql.ErrNoRows:
			sources = append(sources, "Source "+s.Name+" is no longer in this close.")
		case err != nil:
			return nil, nil, err
		case replacement.Valid:
			sources = append(sources, "Source "+s.Name+" was replaced by #"+replacement.String+": "+reason)
		case digest != s.Digest:
			sources = append(sources, "Source "+s.Name+" changed since the draft was prepared.")
		}
	}
	parents := []string{}
	for _, link := range []*lineageLink{paper.CarryForward, paper.RecurringAccrual} {
		if link == nil || link.PaperID == "" {
			continue
		}
		var state, digest, review string
		err := db.QueryRowContext(ctx, `SELECT review_state,digest,review_id FROM workpaper_review_state WHERE id::text=$1`, link.PaperID).Scan(&state, &digest, &review)
		if err != nil && err != sql.ErrNoRows {
			return nil, nil, err
		}
		if err == sql.ErrNoRows || state != "approved" || digest != link.Digest || review != link.ReviewID {
			parents = append(parents, "The schedule it carries from, draft #"+link.PaperID+", is no longer the approved version it was built from.")
		}
	}
	return sources, parents, nil
}

// anchorLabel names what a comment is about, or reports false when the anchor is not part of this draft.
func anchorLabel(ctx context.Context, tx *sql.Tx, closeID, anchor string, raw []byte) (string, bool) {
	m := commentAnchor.FindStringSubmatch(anchor)
	if m == nil {
		return "", false
	}
	var paper struct {
		Input   map[string]json.RawMessage `json:"input"`
		Sources []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"sources"`
	}
	json.Unmarshal(raw, &paper)
	switch {
	case m[1] == "general":
		return "The whole draft", true
	case m[2] != "":
		_, ok := paper.Input[m[2]]
		return "Input: " + strings.ReplaceAll(m[2], "_", " "), ok
	}
	i := slices.IndexFunc(paper.Sources, func(s struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}) bool {
		return s.ID == m[3]
	})
	line, _ := strconv.Atoi(m[4])
	var lines int
	if i < 0 || line < 1 || tx.QueryRowContext(ctx, `SELECT array_length(string_to_array(content,E'\n'),1) FROM evidence WHERE id::text=$1 AND close_id::text=$2`, m[3], closeID).Scan(&lines) != nil || line > lines {
		return "", false
	}
	return fmt.Sprintf("%s, line %d", paper.Sources[i].Name, line), true
}
