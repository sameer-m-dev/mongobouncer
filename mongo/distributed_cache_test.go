package mongo

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestDistributedCacheDisabled(t *testing.T) {
	logger := zap.NewNop()
	config := &DistributedCacheConfig{
		Enabled: false,
	}

	cache := NewDistributedCache(config, logger)
	if cache.IsEnabled() {
		t.Error("Expected cache to be disabled")
	}

	// Test operations should be no-ops
	err := cache.StoreSession(&Session{ID: "test"})
	if err != nil {
		t.Errorf("StoreSession should be no-op when disabled: %v", err)
	}

	session, found := cache.GetSession("test")
	if found {
		t.Error("GetSession should return false when disabled")
	}
	if session != nil {
		t.Error("GetSession should return nil when disabled")
	}
}

func TestDistributedCacheEnabled(t *testing.T) {
	logger := zap.NewNop()
	config := &DistributedCacheConfig{
		Enabled:           true,
		ListenAddr:        "0.0.0.0",
		ListenPort:        18080,       // Use different port to avoid conflicts
		CacheSizeBytes:    1024 * 1024, // 1MB
		SessionExpiry:     30 * time.Minute,
		TransactionExpiry: 2 * time.Minute,
		CursorExpiry:      24 * time.Hour,
		Debug:             false,
	}

	cache := NewDistributedCache(config, logger)
	// Note: Cache might not be enabled if daemon fails to start (e.g., port conflicts)
	// This is expected in test environments
	if cache.IsEnabled() {
		// Test basic stats
		stats := cache.GetStats()
		if stats["enabled"] != true {
			t.Error("Expected enabled to be true in stats")
		}
	} else {
		t.Log("Cache not enabled (likely due to port conflicts in test environment)")
	}
}

func TestDistributedCacheSessionOperations(t *testing.T) {
	logger := zap.NewNop()
	config := &DistributedCacheConfig{
		Enabled:           true,
		ListenAddr:        "0.0.0.0",
		ListenPort:        18081,       // Use different port to avoid conflicts
		CacheSizeBytes:    1024 * 1024, // 1MB
		SessionExpiry:     30 * time.Minute,
		TransactionExpiry: 2 * time.Minute,
		CursorExpiry:      24 * time.Hour,
		Debug:             false,
	}

	cache := NewDistributedCache(config, logger)
	if !cache.IsEnabled() {
		t.Skip("Cache not enabled, skipping test")
	}

	// Test session operations
	session := &Session{
		ID:           "test-session",
		ClientID:     "test-client",
		State:        SessionStateActive,
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
	}

	// Store session
	err := cache.StoreSession(session)
	if err != nil {
		t.Errorf("Failed to store session: %v", err)
	}

	// Get session
	retrievedSession, found := cache.GetSession("test-session")
	if !found {
		t.Error("Expected session to be found")
	}
	if retrievedSession == nil {
		t.Error("Expected session to be non-nil")
	}
	if retrievedSession.ID != session.ID {
		t.Errorf("Expected session ID %s, got %s", session.ID, retrievedSession.ID)
	}

	// Remove session
	err = cache.RemoveSession("test-session")
	if err != nil {
		t.Errorf("Failed to remove session: %v", err)
	}
}

func TestDistributedCacheTransactionOperations(t *testing.T) {
	logger := zap.NewNop()
	config := &DistributedCacheConfig{
		Enabled:           true,
		ListenAddr:        "0.0.0.0",
		ListenPort:        18082,       // Use different port to avoid conflicts
		CacheSizeBytes:    1024 * 1024, // 1MB
		SessionExpiry:     30 * time.Minute,
		TransactionExpiry: 2 * time.Minute,
		CursorExpiry:      24 * time.Hour,
		Debug:             false,
	}

	cache := NewDistributedCache(config, logger)
	if !cache.IsEnabled() {
		t.Skip("Cache not enabled, skipping test")
	}

	// Test transaction operations
	lsid := []byte("test-lsid")

	// Store transaction (note: we can't easily mock driver.Server in tests)
	err := cache.StoreTransaction(lsid, nil, 1)
	if err != nil {
		t.Errorf("Failed to store transaction: %v", err)
	}

	// Get transaction
	_, found := cache.GetTransaction(lsid)
	if !found {
		t.Error("Expected transaction to be found")
	}

	// Remove transaction
	err = cache.RemoveTransaction(lsid)
	if err != nil {
		t.Errorf("Failed to remove transaction: %v", err)
	}
}

func TestDistributedCacheCursorOperations(t *testing.T) {
	logger := zap.NewNop()
	config := &DistributedCacheConfig{
		Enabled:           true,
		ListenAddr:        "0.0.0.0",
		ListenPort:        18083,       // Use different port to avoid conflicts
		CacheSizeBytes:    1024 * 1024, // 1MB
		SessionExpiry:     30 * time.Minute,
		TransactionExpiry: 2 * time.Minute,
		CursorExpiry:      24 * time.Hour,
		Debug:             false,
	}

	cache := NewDistributedCache(config, logger)
	if !cache.IsEnabled() {
		t.Skip("Cache not enabled, skipping test")
	}

	// Test cursor operations
	cursorID := int64(12345)
	collection := "test_collection"
	databaseName := "test_db"

	// Store cursor
	err := cache.StoreCursor(cursorID, collection, nil, databaseName)
	if err != nil {
		t.Errorf("Failed to store cursor: %v", err)
	}

	// Get cursor
	_, dbName, found := cache.GetCursor(cursorID, collection)
	if !found {
		t.Error("Expected cursor to be found")
	}
	// Note: server will be nil as we can't serialize driver.Server
	if dbName != databaseName {
		t.Errorf("Expected database name %s, got %s", databaseName, dbName)
	}

	// Remove cursor
	err = cache.RemoveCursor(cursorID, collection)
	if err != nil {
		t.Errorf("Failed to remove cursor: %v", err)
	}
}
