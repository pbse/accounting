package transport

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"close.local/backend/internal/auth"
)

// Google Drive is read through WorkOS Pipes, which holds each person's grant; it is off without WORKOS_API_KEY.
const driveSlug = "google-drive"

var driveTypes = map[string]string{"application/pdf": ".pdf", "text/plain": ".txt", "text/csv": ".csv"}
var driveFileID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
var driveClient = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func driveCall(ctx context.Context, method, target, bearer string, body any, out any, limit int64) ([]byte, error) {
	var payload io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := driveClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s returned %d", req.URL.Host, resp.StatusCode)
	}
	if int64(len(raw)) > limit {
		return nil, errTooLarge
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return nil, errors.New("unreadable response")
	}
	return raw, nil
}

var errTooLarge = errors.New("too large")

// driveToken returns the person's Drive access token, or why they must connect first.
func driveToken(ctx context.Context, a *auth.Claims) (string, string, error) {
	var result struct {
		Active      bool   `json:"active"`
		Error       string `json:"error"`
		AccessToken struct {
			AccessToken   string   `json:"access_token"`
			MissingScopes []string `json:"missing_scopes"`
		} `json:"access_token"`
	}
	if _, err := driveCall(ctx, "POST", "https://api.workos.com/data-integrations/"+driveSlug+"/token", os.Getenv("WORKOS_API_KEY"), map[string]string{"user_id": a.Subject, "organization_id": a.Organization}, &result, 1<<16); err != nil {
		return "", "", err
	}
	switch {
	case !result.Active && result.Error == "needs_reauthorization":
		return "", "Reconnect Google Drive.", nil
	case !result.Active:
		return "", "Connect Google Drive first.", nil
	case len(result.AccessToken.MissingScopes) > 0:
		return "", "Reconnect Google Drive to grant " + strings.Join(result.AccessToken.MissingScopes, ", ") + ".", nil
	case result.AccessToken.AccessToken == "":
		return "", "", errors.New("no access token")
	}
	return result.AccessToken.AccessToken, "", nil
}

func driveActor(r *http.Request) *auth.Claims {
	if a := auth.Actor(r.Context()); os.Getenv("WORKOS_API_KEY") != "" && a != nil && a.Subject != "" && a.Organization != "" {
		return a
	}
	return nil
}

// driveName gives a Drive file the extension evidence upload expects for its type.
func driveName(name, mime string) string {
	ext := driveTypes[mime]
	if strings.ToLower(filepath.Ext(name)) != ext {
		name += ext
	}
	return name
}

func registerDrive(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/closes/{id}/drive", func(w http.ResponseWriter, r *http.Request) {
		a := driveActor(r)
		if a == nil {
			jsonReply(w, 200, map[string]any{"configured": false})
			return
		}
		token, reason, err := driveToken(r.Context(), a)
		if err != nil {
			problem(w, 502, "Google Drive is unavailable. Upload the file directly instead.")
			return
		}
		if reason != "" {
			jsonReply(w, 200, map[string]any{"configured": true, "connected": false, "reason": reason})
			return
		}
		search := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(search) > 100 {
			problem(w, 422, "Search for up to 100 characters.")
			return
		}
		query := "trashed=false and (mimeType='application/pdf' or mimeType='text/plain' or mimeType='text/csv')"
		if search != "" {
			query += " and name contains '" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(search) + "'"
		}
		var list struct {
			Files []struct {
				ID, Name, MimeType, Size, ModifiedTime string
			} `json:"files"`
		}
		params := url.Values{"q": {query}, "fields": {"files(id,name,mimeType,size,modifiedTime)"}, "pageSize": {"25"}, "orderBy": {"modifiedTime desc"}}
		if _, err = driveCall(r.Context(), "GET", "https://www.googleapis.com/drive/v3/files?"+params.Encode(), token, nil, &list, 1<<20); err != nil {
			problem(w, 502, "Google Drive is unavailable. Upload the file directly instead.")
			return
		}
		files := []map[string]string{}
		for _, f := range list.Files {
			if driveTypes[f.MimeType] != "" && driveFileID.MatchString(f.ID) {
				files = append(files, map[string]string{"id": f.ID, "name": f.Name, "filename": driveName(f.Name, f.MimeType), "size": f.Size, "modified": f.ModifiedTime})
			}
		}
		jsonReply(w, 200, map[string]any{"configured": true, "connected": true, "files": files})
	})
	mux.HandleFunc("POST /v1/closes/{id}/drive/connect", func(w http.ResponseWriter, r *http.Request) {
		a := driveActor(r)
		origin, err := url.Parse(os.Getenv("PUBLIC_APP_URL"))
		if a == nil || err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
			problem(w, 409, "Google Drive is not configured.")
			return
		}
		origin.Path, origin.RawQuery = "/dashboard/closes/"+r.PathValue("id"), "tab=evidence"
		var result struct {
			URL string `json:"url"`
		}
		if _, err = driveCall(r.Context(), "POST", "https://api.workos.com/data-integrations/"+driveSlug+"/authorize", os.Getenv("WORKOS_API_KEY"), map[string]string{"user_id": a.Subject, "organization_id": a.Organization, "return_to": origin.String()}, &result, 1<<16); err != nil || !strings.HasPrefix(result.URL, "https://") {
			problem(w, 502, "Google Drive connection is unavailable.")
			return
		}
		jsonReply(w, 200, map[string]string{"url": result.URL})
	})
	// The file's bytes go back to the browser, which adds them through the ordinary evidence upload.
	mux.HandleFunc("GET /v1/closes/{id}/drive/files/{file}", func(w http.ResponseWriter, r *http.Request) {
		a := driveActor(r)
		if a == nil || !driveFileID.MatchString(r.PathValue("file")) {
			problem(w, 404, "Drive file not found.")
			return
		}
		token, reason, err := driveToken(r.Context(), a)
		if err != nil || reason != "" {
			problem(w, 409, cmp.Or(reason, "Google Drive is unavailable. Upload the file directly instead."))
			return
		}
		base := "https://www.googleapis.com/drive/v3/files/" + r.PathValue("file")
		var meta struct{ Name, MimeType string }
		if _, err = driveCall(r.Context(), "GET", base+"?fields=name,mimeType", token, nil, &meta, 1<<16); err != nil {
			problem(w, 502, "Google Drive is unavailable. Upload the file directly instead.")
			return
		}
		if driveTypes[meta.MimeType] == "" {
			problem(w, 422, "Choose a PDF, text or CSV file; Google Docs and other types are not supported.")
			return
		}
		limit := int64(512 * 1024)
		if meta.MimeType == "application/pdf" {
			limit = 5 * 1024 * 1024
		}
		name := driveName(meta.Name, meta.MimeType)
		body, err := driveCall(r.Context(), "GET", base+"?alt=media", token, nil, nil, limit)
		if errors.Is(err, errTooLarge) {
			problem(w, 413, "Choose text/CSV up to 512 KiB or a PDF up to 5 MiB.")
			return
		}
		if err != nil {
			problem(w, 502, "Google Drive is unavailable. Upload the file directly instead.")
			return
		}
		if !validEvidenceFile(name, body) {
			problem(w, 422, "Choose nonempty UTF-8 text/CSV (512 KiB maximum) or a complete PDF (5 MiB maximum).")
			return
		}
		w.Header().Set("Content-Type", meta.MimeType)
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
		w.Write(body)
	})
}
