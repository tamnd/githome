package search

import (
	"reflect"
	"slices"
	"testing"
)

func TestParseTermsAndQualifiers(t *testing.T) {
	q := Parse("crash bug repo:octocat/hello state:open label:urgent")
	if want := []string{"crash", "bug"}; !reflect.DeepEqual(q.Terms, want) {
		t.Errorf("terms = %v, want %v", q.Terms, want)
	}
	if v, _ := q.First("repo"); v != "octocat/hello" {
		t.Errorf("repo = %q", v)
	}
	if v, _ := q.First("state"); v != "open" {
		t.Errorf("state = %q", v)
	}
	if v, _ := q.First("label"); v != "urgent" {
		t.Errorf("label = %q", v)
	}
}

func TestParseRepeatedQualifier(t *testing.T) {
	q := Parse("label:bug label:urgent")
	if want := []string{"bug", "urgent"}; !reflect.DeepEqual(q.Values("label"), want) {
		t.Errorf("labels = %v, want %v", q.Values("label"), want)
	}
	if len(q.Terms) != 0 {
		t.Errorf("expected no free-text terms, got %v", q.Terms)
	}
}

func TestParseQuotedValueAndPhrase(t *testing.T) {
	q := Parse(`"exact phrase" label:"help wanted"`)
	if want := []string{"exact phrase"}; !reflect.DeepEqual(q.Terms, want) {
		t.Errorf("terms = %v, want %v", q.Terms, want)
	}
	if v, _ := q.First("label"); v != "help wanted" {
		t.Errorf("label = %q, want %q", v, "help wanted")
	}
}

func TestParseUnknownQualifierStaysTerm(t *testing.T) {
	// A colon token whose key is not a known qualifier is a free-text term, so a
	// time of day or a URL is not mistaken for a qualifier.
	q := Parse("see http://example.com at 12:30")
	for _, term := range []string{"see", "http://example.com", "at", "12:30"} {
		if !slices.Contains(q.Terms, term) {
			t.Errorf("missing term %q in %v", term, q.Terms)
		}
	}
	if len(q.Qualifiers) != 0 {
		t.Errorf("expected no qualifiers, got %v", q.Qualifiers)
	}
}

func TestParseKeyOnlyColonIsTerm(t *testing.T) {
	// A leading colon has no key, so it is a plain term, not a qualifier.
	q := Parse(":nope repo:")
	if v, ok := q.First("repo"); !ok || v != "" {
		t.Errorf("repo qualifier with empty value = (%q,%v), want (\"\",true)", v, ok)
	}
	if !slices.Contains(q.Terms, ":nope") {
		t.Errorf("expected :nope as a term, got %v", q.Terms)
	}
}

func TestFieldsDefaultAndSelection(t *testing.T) {
	def := Fields(Parse("hello"), FieldTitle, FieldBody)
	if !reflect.DeepEqual(def, []Field{FieldTitle, FieldBody}) {
		t.Errorf("default fields = %v", def)
	}
	sel := Fields(Parse("hello in:title"), FieldTitle, FieldBody)
	if !reflect.DeepEqual(sel, []Field{FieldTitle}) {
		t.Errorf("in:title fields = %v", sel)
	}
}
