package rest

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// docRoot is the documentation_url GitHub returns on generic errors. Using the
// public docs host keeps error bodies identical to upstream for clients that
// surface the link.
const docRoot = "https://docs.github.com/rest"

// FieldError is one entry in a 422 validation error's errors array. Code is one
// of: missing, missing_field, invalid, already_exists, unprocessable, custom.
type FieldError struct {
	Resource string `json:"resource"`
	Field    string `json:"field"`
	Code     string `json:"code"`
	Message  string `json:"message,omitempty"`
}

// apiError is the internal representation of an error response. It carries the
// HTTP status separately from the JSON body.
type apiError struct {
	Status  int
	Message string
	Errors  []FieldError
	DocURL  string
}

func (e *apiError) Error() string { return e.Message }

// wireError is the JSON projection of an apiError. GitHub copies the numeric
// status into the body as a string on 422 responses, so we do too.
type wireError struct {
	Message string       `json:"message"`
	Errors  []FieldError `json:"errors,omitempty"`
	DocURL  string       `json:"documentation_url,omitempty"`
	Status  string       `json:"status,omitempty"`
}

func writeError(w http.ResponseWriter, e *apiError) {
	body := wireError{Message: e.Message, Errors: e.Errors, DocURL: e.DocURL}
	if e.Status == http.StatusUnprocessableEntity {
		body.Status = strconv.Itoa(e.Status)
	}
	buf, err := json.Marshal(body)
	if err != nil {
		http.Error(w, `{"message":"Server Error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(e.Status)
	_, _ = w.Write(buf)
}

func errNotFound() *apiError {
	return &apiError{Status: http.StatusNotFound, Message: "Not Found", DocURL: docRoot}
}

// errConflict is the 409 GitHub returns when an operation cannot apply to the
// repository's current state, such as listing commits on a repository with no
// commits yet.
func errConflict(message string) *apiError {
	return &apiError{Status: http.StatusConflict, Message: message, DocURL: docRoot}
}

func errValidation(fields ...FieldError) *apiError {
	return &apiError{
		Status:  http.StatusUnprocessableEntity,
		Message: "Validation Failed",
		Errors:  fields,
		DocURL:  docRoot,
	}
}

// errBadCredentials is the 401 for a credential that was presented but is
// invalid, expired, or revoked. GitHub uses the exact message "Bad credentials".
func errBadCredentials() *apiError {
	return &apiError{Status: http.StatusUnauthorized, Message: "Bad credentials", DocURL: docRoot}
}

// errRequiresAuth is the 401 for an endpoint that needs a credential when none
// was presented, such as GET /user for an anonymous caller.
func errRequiresAuth() *apiError {
	return &apiError{
		Status:  http.StatusUnauthorized,
		Message: "Requires authentication",
		DocURL:  "https://docs.github.com/rest/users/users#get-the-authenticated-user",
	}
}
