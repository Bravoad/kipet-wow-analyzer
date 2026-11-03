package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	rdb "github.com/redis/go-redis/v9"
)

type Cache struct{ client *rdb.Client }

func New(rawURL string) (*Cache, error) {
	options, err := rdb.ParseURL(rawURL)
	if err != nil {
		return nil, err
	}
	options.DialTimeout = time.Second
	options.ReadTimeout = time.Second
	options.WriteTimeout = time.Second
	options.MaxRetries = 0
	return &Cache{client: rdb.NewClient(options)}, nil
}
func (c *Cache) Close() error                   { return c.client.Close() }
func (c *Cache) Ping(ctx context.Context) error { return c.client.Ping(ctx).Err() }
func key(snapshot, section, locale string) string {
	return "kipet:v1:snapshot:" + snapshot + ":" + section + ":" + locale
}
func (c *Cache) Get(ctx context.Context, snapshot, section, locale string) ([]byte, error) {
	return c.client.Get(ctx, key(snapshot, section, locale)).Bytes()
}
func (c *Cache) Put(ctx context.Context, snapshot, section, locale string, data []byte, until time.Time) error {
	ttl := min(time.Until(until), 15*time.Minute)
	if ttl <= 0 {
		return nil
	}
	return c.client.Set(ctx, key(snapshot, section, locale), data, ttl).Err()
}

var bucketScript = rdb.NewScript(`
local rate=tonumber(ARGV[1]);local burst=tonumber(ARGV[2]);local per=tonumber(ARGV[3])
local nowParts=redis.call('TIME');local now=tonumber(nowParts[1])*1000+tonumber(nowParts[2])/1000
local old=redis.call('HMGET',KEYS[1],'tokens','at');local tokens=tonumber(old[1]) or burst;local at=tonumber(old[2]) or now
tokens=math.min(burst,tokens+math.max(0,now-at)*rate/per)
local allowed=0;local wait=0;if tokens>=1 then tokens=tokens-1;allowed=1 else wait=math.ceil((1-tokens)*per/rate) end
redis.call('HSET',KEYS[1],'tokens',tokens,'at',now);redis.call('PEXPIRE',KEYS[1],math.ceil(per*burst/rate)+1000)
return {allowed,wait}
`)

func (c *Cache) Allow(ctx context.Context, bucket string, limit, burst int, per time.Duration) (bool, time.Duration, error) {
	hash := sha256.Sum256([]byte(bucket))
	v, err := bucketScript.Run(ctx, c.client, []string{"kipet:v1:limiter:" + hex.EncodeToString(hash[:])}, limit, burst, per.Milliseconds()).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	return v[0] == 1, time.Duration(v[1]) * time.Millisecond, nil
}
