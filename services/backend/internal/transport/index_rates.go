package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"close.local/backend/internal/preparation"
)

var indexPaths = map[string]string{"SOFR": "secured/sofr", "EFFR": "unsecured/effr"}

// refuseIndex answers a loan rate from an index that is not the published value on record, or an unreadable record.
func refuseIndex(w http.ResponseWriter, err error) {
	var mismatch preparation.IndexMismatch
	if errors.As(err, &mismatch) {
		problem(w, 409, err.Error())
		return
	}
	problem(w, 503, "Published index rates are unavailable.")
}

// registerIndexRates looks up the New York Fed's published SOFR or EFFR for a date: the latest value published on or
// in the ten days before it. The value is stored, so a loan's rate from that index is checked against it.
func registerIndexRates(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/index-rates", func(w http.ResponseWriter, r *http.Request) {
		name, day := r.URL.Query().Get("index"), r.URL.Query().Get("date")
		path, known := indexPaths[name]
		date, err := time.Parse("2006-01-02", day)
		if !known || err != nil {
			problem(w, 422, "Choose SOFR or EFFR and a date.")
			return
		}
		source := "https://markets.newyorkfed.org/api/rates/" + path + "/search.json?" + url.Values{"startDate": {date.AddDate(0, 0, -10).Format("2006-01-02")}, "endDate": {day}}.Encode()
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", source, nil)
		req.Header.Set("Accept", "application/json")
		// A redirect is refused, so the answer can only come from the Fed's own host.
		response, err := (&http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			problem(w, 503, "The New York Fed did not answer; try again.")
			return
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 256*1024))
		var body struct {
			RefRates []struct {
				EffectiveDate, Type string
				PercentRate         json.Number
			}
		}
		if err != nil || response.StatusCode != 200 || json.Unmarshal(raw, &body) != nil {
			problem(w, 503, "The New York Fed's answer could not be read; try again.")
			return
		}
		effective, percent := "", json.Number("")
		for _, rate := range body.RefRates {
			if rate.Type == name && rate.EffectiveDate > effective {
				effective, percent = rate.EffectiveDate, rate.PercentRate
			}
		}
		if effective == "" {
			problem(w, 404, "The New York Fed published no "+name+" on or in the ten days before "+day+".")
			return
		}
		// A revised publication replaces the stored value.
		if _, err = db.ExecContext(r.Context(), `INSERT INTO index_rates(name,effective_date,percent) VALUES($1,$2,$3) ON CONFLICT(name,effective_date) DO UPDATE SET percent=excluded.percent,fetched_at=now()`, name, effective, percent.String()); err != nil {
			problem(w, 503, "The published rate could not be stored; try again.")
			return
		}
		jsonReply(w, 200, map[string]string{"index": name, "requested_date": day, "effective_date": effective, "percent": percent.String(), "source": source})
	})
}
