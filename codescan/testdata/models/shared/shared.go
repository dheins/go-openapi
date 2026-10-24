// Package shared holds a type whose name clashes with one in models.
package shared

// Owner is an owner, from another package.
type Owner struct {
	Email string `json:"email" goapi:"format=email"`
}
