package graphql

import (
	"github.com/tamnd/githome/api/graphql/generated"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// strptr returns a pointer to s, the one-liner the static tables below need to
// fill the optional schema fields.
func strptr(s string) *string { return &s }

// uriptr returns a pointer to a URI built from s.
func uriptr(s string) *gqlmodel.URI {
	u := gqlmodel.URI(s)
	return &u
}

// staticLicenses is the set of open-source licenses Githome can name, keyed the
// way GitHub keys them (lowercase). The list mirrors GitHub's most commonly
// selected licenses; bodies are not stored, so License.body comes back empty
// (the summary is enough for the license picker and licenseInfo). The order is
// the order GitHub lists them in the license picker.
var staticLicenses = []*gqlmodel.License{
	{Key: "mit", Name: "MIT License", Nickname: nil, SpdxID: strptr("MIT"), URL: uriptr("https://opensource.org/licenses/MIT")},
	{Key: "apache-2.0", Name: "Apache License 2.0", SpdxID: strptr("Apache-2.0"), URL: uriptr("https://opensource.org/licenses/Apache-2.0")},
	{Key: "gpl-3.0", Name: "GNU General Public License v3.0", Nickname: strptr("GNU GPLv3"), SpdxID: strptr("GPL-3.0"), URL: uriptr("https://www.gnu.org/licenses/gpl-3.0")},
	{Key: "gpl-2.0", Name: "GNU General Public License v2.0", Nickname: strptr("GNU GPLv2"), SpdxID: strptr("GPL-2.0"), URL: uriptr("https://www.gnu.org/licenses/old-licenses/gpl-2.0")},
	{Key: "bsd-3-clause", Name: `BSD 3-Clause "New" or "Revised" License`, SpdxID: strptr("BSD-3-Clause"), URL: uriptr("https://opensource.org/licenses/BSD-3-Clause")},
	{Key: "bsd-2-clause", Name: `BSD 2-Clause "Simplified" License`, SpdxID: strptr("BSD-2-Clause"), URL: uriptr("https://opensource.org/licenses/BSD-2-Clause")},
	{Key: "mpl-2.0", Name: "Mozilla Public License 2.0", SpdxID: strptr("MPL-2.0"), URL: uriptr("https://opensource.org/licenses/MPL-2.0")},
	{Key: "lgpl-2.1", Name: "GNU Lesser General Public License v2.1", Nickname: strptr("GNU LGPLv2.1"), SpdxID: strptr("LGPL-2.1"), URL: uriptr("https://www.gnu.org/licenses/old-licenses/lgpl-2.1")},
	{Key: "agpl-3.0", Name: "GNU Affero General Public License v3.0", Nickname: strptr("GNU AGPLv3"), SpdxID: strptr("AGPL-3.0"), URL: uriptr("https://www.gnu.org/licenses/agpl-3.0")},
	{Key: "unlicense", Name: "The Unlicense", SpdxID: strptr("Unlicense"), URL: uriptr("https://unlicense.org/")},
}

// licenseByKey returns the static license for a GitHub license key, or nil when
// the key is unknown.
func licenseByKey(key string) *gqlmodel.License {
	for _, l := range staticLicenses {
		if l.Key == key {
			return l
		}
	}
	return nil
}

// staticCodesOfConduct is the set of codes of conduct Githome can offer, keyed
// the way GitHub keys them. Bodies are not stored, so CodeOfConduct.body comes
// back null and the url points at the canonical published text.
var staticCodesOfConduct = []*generated.CodeOfConduct{
	{Key: "contributor_covenant", Name: "Contributor Covenant", URL: uriptr("https://www.contributor-covenant.org/version/2/1/code_of_conduct/")},
	{Key: "citizen_code_of_conduct", Name: "Citizen Code of Conduct", URL: uriptr("http://citizencodeofconduct.org/")},
}

// codeOfConductByKey returns the static code of conduct for a key, or nil when
// the key is unknown.
func codeOfConductByKey(key string) *generated.CodeOfConduct {
	for _, c := range staticCodesOfConduct {
		if c.Key == key {
			return c
		}
	}
	return nil
}
