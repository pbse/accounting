// Package close_summary reads where a close stands: its client and month, whether it is signed off,
// and its preparation work by state. It changes nothing.
package close_summary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"close.local/backend/internal/wf"
)

type Input struct {
	CloseID string `json:"close_id" description:"The close to summarise"`
}

type Output struct {
	Entity    string         `json:"entity"`
	Period    string         `json:"period"`
	SignedOff bool           `json:"signed_off"`
	Work      map[string]int `json:"work" description:"Preparation jobs by state"`
}

var Activity = wf.DefineActivity(wf.ActivitySpec[Input, Output]{
	Name:        "close_summary",
	Title:       "Summarise a close",
	Description: "Reads a close's client, month, whether it is signed off and its preparation work by state. Changes nothing.",
	Effect:      wf.Read,
	Run:         summarise,
})

func summarise(ctx context.Context, in Input) (Output, error) {
	var out Output
	var work []byte
	err := wf.DB(ctx).QueryRowContext(ctx, `SELECT c.entity,c.period,
 COALESCE((SELECT decision='signed_off' FROM close_signoffs WHERE close_id=c.id ORDER BY close_signoffs.id DESC LIMIT 1),false),
 COALESCE((SELECT jsonb_object_agg(state,n) FROM (SELECT state,count(*) AS n FROM preparation_jobs WHERE close_id=c.id GROUP BY state) s),'{}'::jsonb)
 FROM client_closes c WHERE c.id::text=$1 AND c.organization_id=$2`, in.CloseID, wf.Organization(ctx)).Scan(&out.Entity, &out.Period, &out.SignedOff, &work)
	if errors.Is(err, sql.ErrNoRows) {
		return out, wf.Permanent(fmt.Errorf("close %s is not one of this firm's", in.CloseID))
	}
	if err == nil {
		err = json.Unmarshal(work, &out.Work)
	}
	return out, err
}
