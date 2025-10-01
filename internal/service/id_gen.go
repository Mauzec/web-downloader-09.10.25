package service

import "github.com/google/uuid"

type IDGenerator interface {
	NewID() (string, error)
}
type RandomIDGenerator struct {
	prefix string
}

func NewRandomIDGenerator(prefix string) *RandomIDGenerator {
	return &RandomIDGenerator{prefix: prefix}
}

// NewID returns random identifier.
func (g *RandomIDGenerator) NewID() (string, error) {
	id := uuid.NewString()
	if g.prefix == "" {
		return id, nil
	}
	return g.prefix + id, nil
}
