package auth

import (
	"errors"
	"time"

	"github.com/dgraph-io/ristretto/v2"
)

type SessionCache interface {
	Get([32]byte) (SessionAccount, bool)
	Set([32]byte, SessionAccount, time.Duration) bool
	Delete([32]byte)
	Close()
}

type RistrettoCache struct {
	cache *ristretto.Cache[string, SessionAccount]
}

func NewRistrettoCache(maxEntries int64) (*RistrettoCache, error) {
	if maxEntries <= 0 {
		return nil, errors.New("session cache capacity must be positive")
	}
	cache, err := ristretto.NewCache(&ristretto.Config[string, SessionAccount]{
		NumCounters:        maxEntries * 10,
		MaxCost:            maxEntries,
		BufferItems:        64,
		IgnoreInternalCost: true,
	})
	if err != nil {
		return nil, err
	}
	return &RistrettoCache{cache: cache}, nil
}

func (c *RistrettoCache) Get(hash [32]byte) (SessionAccount, bool) {
	return c.cache.Get(string(hash[:]))
}

func (c *RistrettoCache) Set(hash [32]byte, value SessionAccount, ttl time.Duration) bool {
	return c.cache.SetWithTTL(string(hash[:]), value, 1, ttl)
}

func (c *RistrettoCache) Delete(hash [32]byte) {
	c.cache.Del(string(hash[:]))
}

func (c *RistrettoCache) Close() {
	c.cache.Close()
}
