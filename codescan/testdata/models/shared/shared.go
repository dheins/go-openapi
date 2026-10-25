// Package shared holds a type whose name clashes with one in models.
package shared

// Owner is an owner, from another package.
type Owner struct {
	Email string `json:"email" goapi:"format=email"`
}

// Omnivore eats anything; a variant of models.Diet from another package.
type Omnivore struct {
	Kind string `json:"kind"`
}

// Eats makes Omnivore a models.Diet.
func (Omnivore) Eats() string { return "anything" }
