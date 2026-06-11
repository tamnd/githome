package git

import (
	"context"
	"strings"
)

// The ref listings used to classify every tag with a per-ref object lookup
// through go-git: each annotated tag paid a tag-object decode, so ten thousand
// tags meant ten thousand object reads per GET /git/refs. One for-each-ref
// subprocess answers name, target, peeled commit, and tagger metadata for the
// whole namespace in a single pass. The go-git paths remain as the fallback
// for a Repo opened without a store (some tests) or a failed subprocess.

// forEachRefFormat is the for-each-ref format the batched ref listings parse:
// ten NUL-separated fields per ref, with a trailing NUL closing the record. A
// message cannot contain NUL, so the NUL split is safe even across multi-line
// messages; the newline git appends after each record then always lands at
// the head of the next refname field, where no ambiguity is possible.
const forEachRefFormat = "%(refname)%00%(objecttype)%00%(objectname)%00%(*objectname)%00%(object)%00%(type)%00%(taggername)%00%(taggeremail)%00%(taggerdate:iso-strict)%00%(contents)%00"

// refRecordFields is how many fields forEachRefFormat emits per ref.
const refRecordFields = 10

// refRecord is one parsed for-each-ref line.
type refRecord struct {
	name       string // refs/heads/main, refs/tags/v1.0
	objType    string // commit, tag
	sha        string // the object the ref names directly
	peeledSHA  string // the commit an annotated tag peels to; empty otherwise
	target     string // the object an annotated tag points at; empty otherwise
	targetType string // that object's type; empty otherwise
	tagger     Signature
	message    string
}

// refRecords runs one for-each-ref pass over the patterns and parses the
// records, in refname order (git's default sort). ok is false when the Repo
// has no store to run subprocesses through or the pass failed, in which case
// the caller falls back to go-git iteration.
func (r *Repo) refRecords(patterns ...string) (recs []refRecord, ok bool) {
	if r.store == nil {
		return nil, false
	}
	args := append([]string{"for-each-ref", "--format=" + forEachRefFormat}, patterns...)
	res, err := r.store.run(context.Background(), r.pk, nil, args...)
	if err != nil || res.code != 0 {
		return nil, false
	}
	flat := strings.Split(string(res.stdout), "\x00")
	// The newline git appends after each record lands at the head of the next
	// record's first field (or alone in the final element, which the chunked
	// loop below never reaches). Refnames cannot contain newlines, so the
	// strip is unambiguous even when a tag message spans lines.
	for i := refRecordFields; i < len(flat); i += refRecordFields {
		f, found := strings.CutPrefix(flat[i], "\n")
		if !found {
			return nil, false
		}
		flat[i] = f
	}
	for i := 0; i+refRecordFields <= len(flat); i += refRecordFields {
		recs = append(recs, refRecord{
			name:       flat[i],
			objType:    flat[i+1],
			sha:        flat[i+2],
			peeledSHA:  flat[i+3],
			target:     flat[i+4],
			targetType: flat[i+5],
			tagger: Signature{
				Name:  flat[i+6],
				Email: strings.Trim(flat[i+7], "<>"),
				When:  parseGitTime(flat[i+8]),
			},
			message: flat[i+9],
		})
	}
	return recs, true
}

// branchesBatch is Branches over one for-each-ref pass.
func (r *Repo) branchesBatch() ([]Branch, bool) {
	recs, ok := r.refRecords("refs/heads")
	if !ok {
		return nil, false
	}
	out := make([]Branch, 0, len(recs))
	for _, rec := range recs {
		out = append(out, Branch{
			Name:   strings.TrimPrefix(rec.name, "refs/heads/"),
			Commit: rec.sha,
		})
	}
	return out, true
}

// tagsBatch is Tags over one for-each-ref pass: the peeled commit and tagger
// metadata arrive in the same records, so an annotated tag costs no extra
// object lookup.
func (r *Repo) tagsBatch() ([]Tag, bool) {
	recs, ok := r.refRecords("refs/tags")
	if !ok {
		return nil, false
	}
	out := make([]Tag, 0, len(recs))
	for _, rec := range recs {
		t := Tag{Name: strings.TrimPrefix(rec.name, "refs/tags/")}
		if rec.objType == "tag" {
			t.Commit = rec.peeledSHA
			t.Annotated = &AnnotatedTag{
				SHA:        rec.sha,
				Tagger:     rec.tagger,
				Message:    rec.message,
				Target:     rec.target,
				TargetType: ObjectType(rec.targetType),
			}
		} else {
			t.Commit = rec.sha
		}
		out = append(out, t)
	}
	return out, true
}

// refsBatch is Refs over one for-each-ref pass.
func (r *Repo) refsBatch() ([]Ref, bool) {
	recs, ok := r.refRecords("refs/heads", "refs/tags")
	if !ok {
		return nil, false
	}
	out := make([]Ref, 0, len(recs))
	for _, rec := range recs {
		typ := ObjectCommit
		if rec.objType == "tag" {
			typ = ObjectTag
		}
		out = append(out, Ref{Name: rec.name, Target: rec.sha, Type: typ})
	}
	return out, true
}
