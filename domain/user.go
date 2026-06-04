// Package domain holds Githome's business logic and value objects. It sits
// between the API surface and the store: handlers call domain services, domain
// services call the store, and the result is a domain value the presenter
// renders. The single dependency path is api -> domain -> store; domain never
// imports api or presenter.
package domain

import "time"

// User is the domain view of an account. It is the presenter's input for both
// the SimpleUser and full User wire models. ID is the public database id
// (users.db_id), not the internal primary key.
type User struct {
	ID              int64
	Login           string
	Type            string
	SiteAdmin       bool
	Name            *string
	Company         *string
	Blog            string
	Location        *string
	Email           *string
	Hireable        *bool
	Bio             *string
	TwitterUsername *string
	PublicRepos     int
	PublicGists     int
	Followers       int
	Following       int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
