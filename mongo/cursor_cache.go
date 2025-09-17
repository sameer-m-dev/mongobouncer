package mongo

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/lruttl"
	"github.com/sameer-m-dev/mongobouncer/util"
)

// on a 64-bit machine, 1 million cursors uses around 480mb of memory
const maxCursors = 1024 * 1024

// one day expiry
const cursorExpiry = 24 * time.Hour

type cursorInfo struct {
	server       driver.Server
	databaseName string
}

type cursorCache struct {
	c                *lruttl.Cache
	metrics          util.MetricsInterface
	log              *zap.Logger
	distributedCache *DistributedCache
}

func newCursorCache() *cursorCache {
	return &cursorCache{
		c: lruttl.New(maxCursors, cursorExpiry),
	}
}

func newCursorCacheWithDistributedCache(distributedCache *DistributedCache) *cursorCache {
	return &cursorCache{
		c:                lruttl.New(maxCursors, cursorExpiry),
		distributedCache: distributedCache,
	}
}

func (c *cursorCache) setMetrics(metrics util.MetricsInterface) {
	c.metrics = metrics
}

func (c *cursorCache) setLogger(log *zap.Logger) {
	c.log = log
}

func (c *cursorCache) count() int {
	return c.c.Len()
}

func (c *cursorCache) peek(cursorID int64, collection string) (server driver.Server, ok bool) {
	// First check local cache
	v, ok := c.c.Peek(buildKey(cursorID, collection))
	if ok {
		cursorInfo := v.(cursorInfo)
		return cursorInfo.server, true
	}

	// If not found locally and distributed cache is enabled, check distributed cache
	if c.distributedCache != nil && c.distributedCache.IsEnabled() {
		if _, _, found := c.distributedCache.GetCursor(cursorID, collection); found {
			// Note: We can't get the actual server object from distributed cache
			// This is a limitation we'll need to handle at a higher level
			return nil, true
		}
	}

	return nil, false
}

func (c *cursorCache) add(cursorID int64, collection string, server driver.Server, databaseName string) {
	info := cursorInfo{
		server:       server,
		databaseName: databaseName,
	}
	c.c.Add(buildKey(cursorID, collection), info)

	// Also store in distributed cache if enabled
	if c.distributedCache != nil && c.distributedCache.IsEnabled() {
		if err := c.distributedCache.StoreCursor(cursorID, collection, server, databaseName); err != nil {
			if c.log != nil {
				c.log.Warn("Failed to store cursor in distributed cache",
					zap.Int64("cursor_id", cursorID),
					zap.String("collection", collection),
					zap.Error(err))
			}
		}
	}
}

func (c *cursorCache) remove(cursorID int64, collection string) {
	key := buildKey(cursorID, collection)

	// Check if the cursor exists before removing
	v, existed := c.c.Peek(key)
	c.c.Remove(key)

	// If the cursor existed and was removed, emit a cursor_closed metric
	if existed && c.metrics != nil {
		cursorInfo := v.(cursorInfo)
		databaseName := cursorInfo.databaseName
		if databaseName == "" {
			databaseName = "default"
		}
		_ = c.metrics.Incr("cursor_closed", []string{fmt.Sprintf("database:%s", databaseName)}, 1)
	}

	// Also remove from distributed cache if enabled
	if c.distributedCache != nil && c.distributedCache.IsEnabled() {
		if err := c.distributedCache.RemoveCursor(cursorID, collection); err != nil {
			if c.log != nil {
				c.log.Warn("Failed to remove cursor from distributed cache",
					zap.Int64("cursor_id", cursorID),
					zap.String("collection", collection),
					zap.Error(err))
			}
		}
	}
}

func buildKey(cursorID int64, collection string) string {
	return fmt.Sprintf("%d-%s", cursorID, collection)
}
