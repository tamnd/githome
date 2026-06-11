package graphql

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// cursorPrefix tags Githome's opaque connection cursor so the scheme can change
// later without ambiguity. Cursors are opaque to clients and gh never inspects
// them. Two forms exist: "gho:<offset>" carries only the absolute offset the
// cursor points just past, and "gho:<offset>:<seek>" additionally carries the
// stable identity of the item at that offset (an issue or pull request number,
// or a comment id), which lets the resolver resume with a keyset seek instead
// of an OFFSET scan. Old offset-only cursors keep decoding, so clients holding
// one across a deploy still page.
const cursorPrefix = "gho:"

// encodeCursor returns the opaque cursor for the item at the given absolute
// offset (zero-based) in the result set.
func encodeCursor(offset int) string {
	raw := cursorPrefix + strconv.Itoa(offset)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

// encodeCursorSeek returns the cursor for the item at the given offset,
// carrying the item's stable id so a follow-up after: resumes via keyset seek.
func encodeCursorSeek(offset int, seek int64) string {
	raw := cursorPrefix + strconv.Itoa(offset) + ":" + strconv.FormatInt(seek, 10)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reads the absolute offset back out of a cursor, rejecting a
// malformed value with GitHub's wording.
func decodeCursor(s string) (int, error) {
	off, _, err := decodeCursorSeek(s)
	return off, err
}

// decodeCursorSeek reads the absolute offset and, when present, the seek id
// back out of a cursor. seek is zero for offset-only cursors.
func decodeCursorSeek(s string) (offset int, seek int64, err error) {
	bad := gqlError{fmt.Sprintf("`%s` does not appear to be a valid cursor.", s)}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return 0, 0, bad
	}
	raw := string(b)
	if !strings.HasPrefix(raw, cursorPrefix) {
		return 0, 0, bad
	}
	body := strings.TrimPrefix(raw, cursorPrefix)
	offPart, seekPart, hasSeek := strings.Cut(body, ":")
	offset, aerr := strconv.Atoi(offPart)
	if aerr != nil || offset < 0 {
		return 0, 0, bad
	}
	if hasSeek {
		seek, aerr = strconv.ParseInt(seekPart, 10, 64)
		if aerr != nil || seek < 0 {
			return 0, 0, bad
		}
	}
	return offset, seek, nil
}
