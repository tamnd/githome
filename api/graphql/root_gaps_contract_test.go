package graphql_test

import (
	"encoding/json"
	"testing"
)

// TestRootStaticQueries covers R02-11: the root query serves license/licenses,
// codeOfConduct/codesOfConduct, and meta. Static-registry lookups return data
// for a known key and null for an unknown one.
func TestRootStaticQueries(t *testing.T) {
	srv, token := graphqlServer(t)

	q := `query{
	  mit: license(key: "mit"){ key name spdxId url body }
	  missing: license(key: "no-such-license"){ key }
	  licenses { key spdxId }
	  cc: codeOfConduct(key: "contributor_covenant"){ key name url }
	  ccMissing: codeOfConduct(key: "no-such-coc"){ key }
	  codesOfConduct { key name }
	  meta { gitHubServicesSha isPasswordAuthenticationVerifiable gitIpAddresses }
	}`
	got := post(t, srv, token, q, nil)
	var env struct {
		Data struct {
			Mit *struct {
				Key    string  `json:"key"`
				Name   string  `json:"name"`
				SpdxID *string `json:"spdxId"`
				URL    *string `json:"url"`
				Body   string  `json:"body"`
			} `json:"mit"`
			Missing  *struct{} `json:"missing"`
			Licenses []struct {
				Key    string  `json:"key"`
				SpdxID *string `json:"spdxId"`
			} `json:"licenses"`
			Cc *struct {
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"cc"`
			CcMissing      *struct{} `json:"ccMissing"`
			CodesOfConduct []struct {
				Key string `json:"key"`
			} `json:"codesOfConduct"`
			Meta struct {
				GitHubServicesSha                  string   `json:"gitHubServicesSha"`
				IsPasswordAuthenticationVerifiable bool     `json:"isPasswordAuthenticationVerifiable"`
				GitIPAddresses                     []string `json:"gitIpAddresses"`
			} `json:"meta"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("errors = %v, body %s", env.Errors, got)
	}
	d := env.Data

	if d.Mit == nil {
		t.Fatalf("license(mit) = null, want the MIT license")
	}
	if d.Mit.Key != "mit" || d.Mit.SpdxID == nil || *d.Mit.SpdxID != "MIT" {
		t.Errorf("license(mit) = %+v, want key mit / spdxId MIT", d.Mit)
	}
	if d.Missing != nil {
		t.Errorf("license(unknown) = %+v, want null", d.Missing)
	}
	if len(d.Licenses) == 0 {
		t.Errorf("licenses is empty, want the static registry")
	}
	if d.Cc == nil || d.Cc.Key != "contributor_covenant" {
		t.Errorf("codeOfConduct(contributor_covenant) = %+v, want the Contributor Covenant", d.Cc)
	}
	if d.CcMissing != nil {
		t.Errorf("codeOfConduct(unknown) = %+v, want null", d.CcMissing)
	}
	if len(d.CodesOfConduct) == 0 {
		t.Errorf("codesOfConduct is empty, want the static registry")
	}
	if !d.Meta.IsPasswordAuthenticationVerifiable {
		t.Errorf("meta.isPasswordAuthenticationVerifiable = false, want true")
	}
}

// TestSearchPageSizeError covers R02-11: search errors when the page size
// exceeds 100 rather than silently clamping, and accepts last/before for
// backward paging. The connection also exposes codeCount and discussionCount.
func TestSearchPageSizeError(t *testing.T) {
	srv, token := graphqlServer(t)

	// first > 100 must surface an error, not a clamped result.
	over := `query{ search(query: "hello", type: REPOSITORY, first: 200){ repositoryCount } }`
	got := post(t, srv, token, over, nil)
	var overEnv struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &overEnv); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if len(overEnv.Errors) == 0 {
		t.Errorf("search(first: 200) returned no error, want a 'less than or equal to 100' error; body %s", got)
	}

	// A valid search exposes the full count surface including codeCount and
	// discussionCount, and accepts last/before without error.
	ok := `query{ search(query: "hello", type: REPOSITORY, last: 10){
	  repositoryCount issueCount userCount wikiCount codeCount discussionCount
	} }`
	got = post(t, srv, token, ok, nil)
	var okEnv struct {
		Data struct {
			Search struct {
				CodeCount       int `json:"codeCount"`
				DiscussionCount int `json:"discussionCount"`
			} `json:"search"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &okEnv); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if len(okEnv.Errors) != 0 {
		t.Fatalf("search(last: 10) errors = %v, body %s", okEnv.Errors, got)
	}
	if okEnv.Data.Search.CodeCount != 0 || okEnv.Data.Search.DiscussionCount != 0 {
		t.Errorf("codeCount/discussionCount = %d/%d, want 0/0 (not indexed)", okEnv.Data.Search.CodeCount, okEnv.Data.Search.DiscussionCount)
	}
}

// TestRepositoryFollowRenames covers R02-11: the repository root field accepts
// the followRenames argument and resolves the repository the same way.
func TestRepositoryFollowRenames(t *testing.T) {
	srv, token := graphqlServer(t)

	q := `query($owner:String!,$name:String!){
	  repository(owner:$owner, name:$name, followRenames: true){ name }
	}`
	got := post(t, srv, token, q, map[string]any{"owner": "octocat", "name": "hello"})
	var env struct {
		Data struct {
			Repository *struct {
				Name string `json:"name"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("errors = %v, body %s", env.Errors, got)
	}
	if env.Data.Repository == nil || env.Data.Repository.Name != "hello" {
		t.Errorf("repository(followRenames:true) = %+v, want repo hello", env.Data.Repository)
	}
}
