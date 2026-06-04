// Package jsondiff makes "100% compatible with GitHub" mechanical. It compares a
// Githome response against a recorded GitHub response (the golden), enforcing
// structure, types, and nullability while ignoring values that legitimately
// vary between instances (ids, timestamps, hosts, rate-limit counters).
//
// The asymmetry is deliberate: every key GitHub sends must be present with the
// same type and nullability, and Githome must not send keys GitHub does not.
// Values are compared only where they carry contract meaning (a resource label,
// a URL path), never where they are instance-specific.
package jsondiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Kind classifies a single difference.
type Kind int

// The kinds of difference Compare can report.
const (
	MissingKey     Kind = iota // present in want, absent in got
	ExtraKey                   // present in got, absent in want
	TypeMismatch               // different JSON types
	Nullability                // one is null, the other is not
	ValueMismatch              // values that must match do not
	LengthMismatch             // arrays of different length
)

func (k Kind) String() string {
	switch k {
	case MissingKey:
		return "missing key"
	case ExtraKey:
		return "extra key"
	case TypeMismatch:
		return "type mismatch"
	case Nullability:
		return "nullability"
	case ValueMismatch:
		return "value mismatch"
	case LengthMismatch:
		return "length mismatch"
	default:
		return "unknown"
	}
}

// Diff is one structural or value difference found by Compare.
type Diff struct {
	Path string
	Kind Kind
	Want string
	Got  string
}

func (d Diff) String() string {
	return fmt.Sprintf("%s at %s: want %q got %q", d.Kind, d.Path, d.Want, d.Got)
}

// Options controls normalization during comparison.
type Options struct {
	// Hosts are rewritten to the sentinel "HOST" before comparing URL strings, so
	// a Githome link compares equal to the captured github.com link by path.
	Hosts []string
	// IgnoreValueKeys names keys whose value varies between instances; presence
	// and type are still enforced, only the value is skipped.
	IgnoreValueKeys map[string]bool
}

// Default returns Options that rewrite the upstream hosts plus the given test
// host, and ignore the value of instance-specific keys.
func Default(testHost string) Options {
	hosts := []string{"github" + ".com"}
	if testHost != "" {
		hosts = append(hosts, testHost)
	}
	return Options{
		Hosts: hosts,
		IgnoreValueKeys: map[string]bool{
			"node_id":   true,
			"reset":     true,
			"remaining": true,
			"used":      true,
			"limit":     true,
		},
	}
}

func (o Options) ignoreValue(key string) bool {
	if o.IgnoreValueKeys[key] {
		return true
	}
	return key == "id" || strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_at")
}

// Compare reports every difference of got against want (the golden).
func Compare(want, got any, opt Options) []Diff {
	return diffValue("$", "", want, got, opt)
}

func diffValue(path, key string, want, got any, opt Options) []Diff {
	if want == nil && got == nil {
		return nil
	}
	if want == nil || got == nil {
		return []Diff{{Path: path, Kind: Nullability, Want: render(want), Got: render(got)}}
	}
	wk, gk := kindOf(want), kindOf(got)
	if wk != gk {
		return []Diff{{Path: path, Kind: TypeMismatch, Want: wk, Got: gk}}
	}
	switch wk {
	case "object":
		return diffObject(path, want.(map[string]any), got.(map[string]any), opt)
	case "array":
		return diffArray(path, key, want.([]any), got.([]any), opt)
	default:
		return diffLeaf(path, key, want, got, opt)
	}
}

func diffObject(path string, want, got map[string]any, opt Options) []Diff {
	var diffs []Diff
	for _, k := range sortedKeys(want) {
		child := path + "." + k
		gv, ok := got[k]
		if !ok {
			diffs = append(diffs, Diff{Path: child, Kind: MissingKey, Want: render(want[k])})
			continue
		}
		diffs = append(diffs, diffValue(child, k, want[k], gv, opt)...)
	}
	for _, k := range sortedKeys(got) {
		if _, ok := want[k]; !ok {
			diffs = append(diffs, Diff{Path: path + "." + k, Kind: ExtraKey, Got: render(got[k])})
		}
	}
	return diffs
}

func diffArray(path, key string, want, got []any, opt Options) []Diff {
	if len(want) != len(got) {
		return []Diff{{Path: path, Kind: LengthMismatch, Want: fmt.Sprint(len(want)), Got: fmt.Sprint(len(got))}}
	}
	var diffs []Diff
	for i := range want {
		diffs = append(diffs, diffValue(fmt.Sprintf("%s[%d]", path, i), key, want[i], got[i], opt)...)
	}
	return diffs
}

func diffLeaf(path, key string, want, got any, opt Options) []Diff {
	if opt.ignoreValue(key) {
		return nil
	}
	ws, wIsStr := want.(string)
	gs, gIsStr := got.(string)
	if wIsStr && gIsStr {
		if normalizeURL(ws, opt) == normalizeURL(gs, opt) {
			return nil
		}
		return []Diff{{Path: path, Kind: ValueMismatch, Want: ws, Got: gs}}
	}
	if render(want) != render(got) {
		return []Diff{{Path: path, Kind: ValueMismatch, Want: render(want), Got: render(got)}}
	}
	return nil
}

// normalizeURL rewrites known hosts to a sentinel so links compare by path. A
// non-URL string is returned unchanged.
func normalizeURL(s string, opt Options) string {
	if !strings.Contains(s, "://") {
		return s
	}
	out := s
	for _, h := range opt.Hosts {
		out = strings.ReplaceAll(out, "://"+h, "://HOST")
		out = strings.ReplaceAll(out, "@"+h, "@HOST")
	}
	return out
}

func kindOf(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "bool"
	case float64, json.Number:
		return "number"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func render(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// AssertCompatible decodes want and got and fails t with every difference. It
// also runs a blunt host-leak backstop on the raw got bytes.
func AssertCompatible(t testing.TB, want, got []byte, opt Options) {
	t.Helper()
	wv := decode(t, "want", want)
	gv := decode(t, "got", got)
	for _, d := range Compare(wv, gv, opt) {
		t.Errorf("incompatible: %s", d)
	}
	for _, leak := range [][]byte{[]byte("api." + "github.com"), []byte("://" + "github.com")} {
		if bytes.Contains(got, leak) {
			t.Errorf("response leaked upstream host literal %q", leak)
		}
	}
}

func decode(t testing.TB, label string, b []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("jsondiff: decode %s: %v", label, err)
	}
	return v
}
