package catalogcache

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/catalogslice"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// keyPrefix names the slice format; a change that old instances cannot read needs a new version.
const keyPrefix = "optimizer:slice:v2:"

func key(city string, revision domain.CatalogRevision) string {
	return keyPrefix + city + ":" + strconv.FormatInt(int64(revision), 10)
}

// RedisStore keeps slices in Redis for other optimizer instances and for restarts.
type RedisStore struct {
	// The client exists only once the Redis component has started.
	client func() goredis.UniversalClient
	ttl    time.Duration
}

func NewRedisStore(client func() goredis.UniversalClient, ttl time.Duration) *RedisStore {
	return &RedisStore{client: client, ttl: ttl}
}

var errRedisNotConnected = errors.New("redis is not connected")

func (r *RedisStore) Get(ctx context.Context, city string, revision domain.CatalogRevision) (*catalogslice.Slice, bool, error) {
	client := r.client()
	if client == nil {
		return nil, false, errRedisNotConnected
	}
	data, err := client.Get(ctx, key(city, revision)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	s, err := decode(data)
	if err != nil {
		return nil, false, err
	}
	return s, true, nil
}

func (r *RedisStore) Put(ctx context.Context, s *catalogslice.Slice) error {
	client := r.client()
	if client == nil {
		return errRedisNotConnected
	}
	data, err := encode(s)
	if err != nil {
		return err
	}
	return client.Set(ctx, key(s.City, s.Revision), data, r.ttl).Err()
}

// JSON keeps what gob loses: a pointer to zero and an empty list stay as they were, not nil.
func encode(s *catalogslice.Slice) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(s); err != nil {
		return nil, fmt.Errorf("encode catalog slice: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("compress catalog slice: %w", err)
	}
	return buf.Bytes(), nil
}

func decode(data []byte) (*catalogslice.Slice, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decompress catalog slice: %w", err)
	}
	var s catalogslice.Slice
	if err := json.NewDecoder(zr).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode catalog slice: %w", err)
	}
	if _, err := io.Copy(io.Discard, zr); err != nil {
		return nil, fmt.Errorf("decompress catalog slice: %w", err)
	}
	return &s, nil
}
