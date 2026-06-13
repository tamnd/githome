package rest

import (
	"mime"
	"net/http"
	"sort"
	"strings"

	"github.com/tamnd/githome/presenter/restmodel"
	"github.com/tamnd/githome/search"
)

// wantsTextMatch reports whether the request's Accept header asks for the
// text-match media type, the signal GitHub uses to enrich search hits with
// text_matches metadata. The header may list several media types; any one of
// them naming the text-match type turns the metadata on.
func wantsTextMatch(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		mt, _, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err == nil && mt == "application/vnd.github.text-match+json" {
			return true
		}
	}
	return false
}

// queryTerms parses the free-text terms out of a raw search query, dropping the
// qualifiers (repo:, user:, label:, ...) so only the words a text match can
// highlight remain.
func queryTerms(raw string) []string {
	return search.Parse(raw).Terms
}

// textMatchElements finds every case-insensitive occurrence of any term within
// fragment and returns them in fragment order, deduped, with the matched text
// and its [start, end) rune offsets. The offsets are rune indices so they line
// up with the way clients slice the fragment string.
func textMatchElements(fragment string, terms []string) []restmodel.TextMatchElement {
	runes := []rune(fragment)
	lower := []rune(strings.ToLower(fragment))
	seen := make(map[[2]int]bool)
	var els []restmodel.TextMatchElement
	for _, term := range terms {
		lt := []rune(strings.ToLower(term))
		if len(lt) == 0 {
			continue
		}
		for i := 0; i+len(lt) <= len(lower); i++ {
			if !runesHasPrefix(lower[i:], lt) {
				continue
			}
			key := [2]int{i, i + len(lt)}
			if seen[key] {
				continue
			}
			seen[key] = true
			els = append(els, restmodel.TextMatchElement{
				Text:    string(runes[i : i+len(lt)]),
				Indices: []int{key[0], key[1]},
			})
		}
	}
	sort.Slice(els, func(a, b int) bool { return els[a].Indices[0] < els[b].Indices[0] })
	return els
}

// runesHasPrefix reports whether s begins with prefix.
func runesHasPrefix(s, prefix []rune) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range prefix {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

// textMatch builds one text_matches entry for a property whose fragment
// contains at least one of the terms, returning false when none match so the
// caller omits the property.
func textMatch(objectURL string, objectType *string, property, fragment string, terms []string) (restmodel.TextMatch, bool) {
	els := textMatchElements(fragment, terms)
	if len(els) == 0 {
		return restmodel.TextMatch{}, false
	}
	return restmodel.TextMatch{
		ObjectURL:  objectURL,
		ObjectType: objectType,
		Property:   property,
		Fragment:   fragment,
		Matches:    els,
	}, true
}

// strptr returns a pointer to s, used for the nullable object_type field.
func strptr(s string) *string { return &s }
