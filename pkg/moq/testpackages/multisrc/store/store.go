package store

import "context"

// Item is a test type.
type Item struct {
	ID string
}

// Store is a test interface.
type Store interface {
	Save(ctx context.Context, item *Item) error
	Get(ctx context.Context, id string) (*Item, error)
}
