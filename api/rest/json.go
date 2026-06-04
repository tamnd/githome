package rest

import (
	"encoding/json"
	"net/http"
)

// writeJSON renders v as a JSON response with the GitHub-standard content type.
func writeJSON(w http.ResponseWriter, status int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		writeError(w, &apiError{Status: http.StatusInternalServerError, Message: "Server Error", DocURL: docRoot})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}
