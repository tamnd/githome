package graphql

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// cursorPrefix tags Githome's opaque connection cursor so the scheme can change
// later without ambiguity. Cursors are opaque to clients and gh never inspects
// them; Githome encodes the absolute offset the cursor points just past, which
// is all the offset-paged issue connection needs to resume.
const cursorPrefix = "gho:"

// encodeCursor returns the opaque cursor for the item at the given absolute
// offset (zero-based) in the result set.
func encodeCursor(offset int) string {
	raw := cursorPrefix + strconv.Itoa(offset)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reads the absolute offset back out of a cursor, rejecting a
// malformed value with GitHub's wording.
func decodeCursor(s string) (int, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return 0, gqlError{fmt.Sprintf("`%s` does not appear to be a valid cursor.", s)}
	}
	raw := string(b)
	if !strings.HasPrefix(raw, cursorPrefix) {
		return 0, gqlError{fmt.Sprintf("`%s` does not appear to be a valid cursor.", s)}
	}
	n, err := strconv.Atoi(strings.TrimPrefix(raw, cursorPrefix))
	if err != nil || n < 0 {
		return 0, gqlError{fmt.Sprintf("`%s` does not appear to be a valid cursor.", s)}
	}
	return n, nil
}
