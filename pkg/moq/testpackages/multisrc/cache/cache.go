package cache

// Cache is a test interface.
type Cache interface {
	Put(key, value string)
	Get(key string) (string, bool)
}
