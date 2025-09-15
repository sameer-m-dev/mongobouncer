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
	c       *lruttl.Cache
	metrics util.MetricsInterface
	log     *zap.Logger
}

func newCursorCache() *cursorCache {
	return &cursorCache{
		c: lruttl.New(maxCursors, cursorExpiry),
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
	v, ok := c.c.Peek(buildKey(cursorID, collection))
	if !ok {
		return
	}
	cursorInfo := v.(cursorInfo)
	return cursorInfo.server, true
}

func (c *cursorCache) add(cursorID int64, collection string, server driver.Server, databaseName string) {
	info := cursorInfo{
		server:       server,
		databaseName: databaseName,
	}
	c.c.Add(buildKey(cursorID, collection), info)
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
}

func buildKey(cursorID int64, collection string) string {
	return fmt.Sprintf("%d-%s", cursorID, collection)
}
