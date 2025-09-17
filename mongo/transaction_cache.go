package mongo

import (
	b64 "encoding/base64"
	"time"

	"github.com/sameer-m-dev/mongobouncer/lruttl"
	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.uber.org/zap"
)

// on a 64-bit machine, 1 million cursors uses around 480mb of memory
const maxTransactions = 1024 * 1024

// 120 seconds default
const transactionExpiry = 120 * time.Second

type transactionCache struct {
	c                *lruttl.Cache
	distributedCache *DistributedCache
	logger           *zap.Logger
}

func newTransactionCache() *transactionCache {
	return &transactionCache{c: lruttl.New(maxTransactions, transactionExpiry)}
}

func newTransactionCacheWithDistributedCache(distributedCache *DistributedCache, logger *zap.Logger) *transactionCache {
	return &transactionCache{
		c:                lruttl.New(maxTransactions, transactionExpiry),
		distributedCache: distributedCache,
		logger:           logger,
	}
}

func (t *transactionCache) count() int {
	return t.c.Len()
}

func (t *transactionCache) peek(lsID []byte) (server driver.Server, ok bool) {
	// First check local cache
	v, ok := t.c.Peek(b64.StdEncoding.EncodeToString(lsID))
	if ok && v != nil {
		return v.(driver.Server), true
	}

	// If not found locally and distributed cache is enabled, check distributed cache
	if t.distributedCache != nil && t.distributedCache.IsEnabled() {
		if _, found := t.distributedCache.GetTransaction(lsID); found {
			// Note: We can't get the actual server object from distributed cache
			// This is a limitation we'll need to handle at a higher level
			return nil, true
		}
	}

	return nil, false
}

func (t *transactionCache) add(lsID []byte, server driver.Server) {
	t.c.Add(b64.StdEncoding.EncodeToString(lsID), server)

	// Also store in distributed cache if enabled
	if t.distributedCache != nil && t.distributedCache.IsEnabled() {
		if err := t.distributedCache.StoreTransaction(lsID, server, 0); err != nil {
			if t.logger != nil {
				t.logger.Warn("Failed to store transaction in distributed cache",
					zap.String("lsid", b64.StdEncoding.EncodeToString(lsID)),
					zap.Error(err))
			}
		}
	}
}

func (t *transactionCache) remove(lsID []byte) {
	t.c.Remove(b64.StdEncoding.EncodeToString(lsID))

	// Also remove from distributed cache if enabled
	if t.distributedCache != nil && t.distributedCache.IsEnabled() {
		if err := t.distributedCache.RemoveTransaction(lsID); err != nil {
			if t.logger != nil {
				t.logger.Warn("Failed to remove transaction from distributed cache",
					zap.String("lsid", b64.StdEncoding.EncodeToString(lsID)),
					zap.Error(err))
			}
		}
	}
}
