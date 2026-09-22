package transport

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"close.local/backend/internal/prepaids"
)

func preparePrepaid(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fail := func(status int, code, field, message string) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(prepaids.InputError{Code: code, Field: field, Message: message})
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		fail(415, "INVALID_REQUEST", "", "Send an application/json request.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input prepaids.Input
	if err := decoder.Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(413, "REQUEST_TOO_LARGE", "", "The request exceeds 16 KiB.")
		} else {
			fail(400, "INVALID_REQUEST", "", "Provide valid JSON with only the supported input fields.")
		}
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		fail(400, "INVALID_REQUEST", "", "Send exactly one JSON object.")
		return
	}
	workpaper, err := prepaids.Prepare(input)
	if err != nil {
		var inputError *prepaids.InputError
		if errors.As(err, &inputError) {
			fail(422, inputError.Code, inputError.Field, inputError.Message)
		} else {
			fail(500, "PREPARATION_FAILED", "", "The workpaper could not be prepared.")
		}
		return
	}
	json.NewEncoder(w).Encode(workpaper)
}
