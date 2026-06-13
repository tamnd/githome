package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/tamnd/githome/domain"
)

// typedError carries a user-facing GraphQL error message together with
// GitHub's machine-readable error type (NOT_FOUND, FORBIDDEN, ...). gh's
// GraphQLError.Match reads the type to tolerate expected failures, so the
// presenter lifts it onto the wire error.
type typedError struct {
	msg     string
	errType string
}

func (e typedError) Error() string { return e.msg }

// notFoundf builds a NOT_FOUND error with GitHub's message wording. A private
// object the viewer cannot see also reports NOT_FOUND, never FORBIDDEN, so
// its existence does not leak.
func notFoundf(format string, args ...any) error {
	return typedError{msg: fmt.Sprintf(format, args...), errType: "NOT_FOUND"}
}

// unprocessablef builds an UNPROCESSABLE error with the given message. GitHub
// returns this type when a mutation is understood but cannot be carried out —
// the faithful answer for a feature Githome does not implement, rather than a
// fake success that reports a change it never made.
func unprocessablef(format string, args ...any) error {
	return typedError{msg: fmt.Sprintf(format, args...), errType: "UNPROCESSABLE"}
}

// notFoundErrors are the domain not-found values the presenter maps to a
// NOT_FOUND type when a resolver lets one through untranslated.
var notFoundErrors = []error{
	domain.ErrRepoNotFound,
	domain.ErrIssueNotFound,
	domain.ErrPullNotFound,
	domain.ErrUserNotFound,
	domain.ErrLabelNotFound,
	domain.ErrMilestoneNotFound,
	domain.ErrCommentNotFound,
	domain.ErrReviewNotFound,
	domain.ErrCheckNotFound,
	domain.ErrRefNotFound,
}

// presentError is gqlgen's SetErrorPresenter hook. It keeps gqlgen's
// path/locations and attaches GitHub's "type" to the extensions; the response
// middleware then lifts it to the top level of the error object, where GitHub
// puts it. Validation errors carry no type, matching GitHub.
func presentError(ctx context.Context, err error) *gqlerror.Error {
	gqlErr := graphql.DefaultErrorPresenter(ctx, err)
	var te typedError
	if errors.As(err, &te) {
		setErrType(gqlErr, te.errType)
		return gqlErr
	}
	for _, nf := range notFoundErrors {
		if errors.Is(err, nf) {
			setErrType(gqlErr, "NOT_FOUND")
			return gqlErr
		}
	}
	if errors.Is(err, domain.ErrForbidden) {
		setErrType(gqlErr, "FORBIDDEN")
	}
	return gqlErr
}

func setErrType(e *gqlerror.Error, t string) {
	if t == "" {
		return
	}
	if e.Extensions == nil {
		e.Extensions = map[string]any{}
	}
	e.Extensions["type"] = t
}

// liftErrorTypes rewrites the GraphQL response body so each error's
// extensions.type becomes a top-level type member. gqlgen can only place
// custom keys under extensions, but GitHub serializes type at the top level
// of the error object and gh matches on it there. Responses without errors
// pass through untouched.
func liftErrorTypes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &bufferingWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		body := rec.buf.Bytes()
		if rewritten, ok := liftTypes(body); ok {
			body = rewritten
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(rec.status)
		_, _ = w.Write(body)
	})
}

// bufferingWriter captures the response so liftErrorTypes can rewrite it. The
// GraphQL endpoint serves small JSON documents, never streams, so buffering
// the body is safe.
type bufferingWriter struct {
	http.ResponseWriter
	buf    bytes.Buffer
	status int
}

func (b *bufferingWriter) WriteHeader(status int) { b.status = status }

func (b *bufferingWriter) Write(p []byte) (int, error) { return b.buf.Write(p) }

// liftTypes moves extensions.type to a top-level type on every error object in
// a GraphQL response body. It reports false when the body has no errors to
// rewrite or does not parse, in which case the caller sends it unchanged.
func liftTypes(body []byte) ([]byte, bool) {
	if !bytes.Contains(body, []byte(`"errors"`)) {
		return nil, false
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, false
	}
	rawErrs, ok := doc["errors"]
	if !ok {
		return nil, false
	}
	var errs []map[string]any
	if err := json.Unmarshal(rawErrs, &errs); err != nil {
		return nil, false
	}
	changed := false
	for _, e := range errs {
		ext, ok := e["extensions"].(map[string]any)
		if !ok {
			continue
		}
		t, ok := ext["type"]
		if !ok {
			continue
		}
		e["type"] = t
		delete(ext, "type")
		if len(ext) == 0 {
			delete(e, "extensions")
		}
		changed = true
	}
	if !changed {
		return nil, false
	}
	newErrs, err := json.Marshal(errs)
	if err != nil {
		return nil, false
	}
	doc["errors"] = newErrs
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false
	}
	return out, true
}
