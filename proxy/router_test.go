package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestDatabaseRouter(t *testing.T) {
	logger := zap.NewNop()

	t.Run("ExactMatch", func(t *testing.T) {
		router := NewDatabaseRouter(logger)
		
		// Add exact routes
		router.AddRoute("mydb", &RouteConfig{DatabaseName: "mydb", Label: "exact"})
		router.AddRoute("testdb", &RouteConfig{DatabaseName: "testdb", Label: "test"})
		
		// Test exact matches
		route, err := router.GetRoute("mydb")
		assert.NoError(t, err)
		assert.Equal(t, "exact", route.Label)
		
		route, err = router.GetRoute("testdb")
		assert.NoError(t, err)
		assert.Equal(t, "test", route.Label)
		
		// Test non-existent
		_, err = router.GetRoute("nonexistent")
		assert.Error(t, err)
	})

	t.Run("WildcardMatch", func(t *testing.T) {
		router := NewDatabaseRouter(logger)
		
		// Add wildcard route
		router.AddRoute("*", &RouteConfig{DatabaseName: "*", Label: "wildcard"})
		
		// Any database should match
		route, err := router.GetRoute("anydb")
		assert.NoError(t, err)
		assert.Equal(t, "wildcard", route.Label)
		
		route, err = router.GetRoute("another_db")
		assert.NoError(t, err)
		assert.Equal(t, "wildcard", route.Label)
	})

	t.Run("PrefixMatch", func(t *testing.T) {
		router := NewDatabaseRouter(logger)
		
		// Add prefix pattern
		router.AddRoute("test_*", &RouteConfig{DatabaseName: "test_*", Label: "test_prefix"})
		router.AddRoute("prod_*", &RouteConfig{DatabaseName: "prod_*", Label: "prod_prefix"})
		
		// Test prefix matches
		route, err := router.GetRoute("test_db1")
		assert.NoError(t, err)
		assert.Equal(t, "test_prefix", route.Label)
		
		route, err = router.GetRoute("test_analytics")
		assert.NoError(t, err)
		assert.Equal(t, "test_prefix", route.Label)
		
		route, err = router.GetRoute("prod_users")
		assert.NoError(t, err)
		assert.Equal(t, "prod_prefix", route.Label)
		
		// Should not match
		_, err = router.GetRoute("dev_test")
		assert.Error(t, err)
	})

	t.Run("SuffixMatch", func(t *testing.T) {
		router := NewDatabaseRouter(logger)
		
		// Add suffix pattern
		router.AddRoute("*_test", &RouteConfig{DatabaseName: "*_test", Label: "test_suffix"})
		router.AddRoute("*_prod", &RouteConfig{DatabaseName: "*_prod", Label: "prod_suffix"})
		
		// Test suffix matches
		route, err := router.GetRoute("myapp_test")
		assert.NoError(t, err)
		assert.Equal(t, "test_suffix", route.Label)
		
		route, err = router.GetRoute("analytics_prod")
		assert.NoError(t, err)
		assert.Equal(t, "prod_suffix", route.Label)
		
		// Should not match
		_, err = router.GetRoute("test_myapp")
		assert.Error(t, err)
	})

	t.Run("ContainsMatch", func(t *testing.T) {
		router := NewDatabaseRouter(logger)
		
		// Add contains pattern
		router.AddRoute("*test*", &RouteConfig{DatabaseName: "*test*", Label: "contains_test"})
		
		// Test contains matches
		route, err := router.GetRoute("mytest_db")
		assert.NoError(t, err)
		assert.Equal(t, "contains_test", route.Label)
		
		route, err = router.GetRoute("db_test_v2")
		assert.NoError(t, err)
		assert.Equal(t, "contains_test", route.Label)
		
		route, err = router.GetRoute("testing")
		assert.NoError(t, err)
		assert.Equal(t, "contains_test", route.Label)
		
		// Should not match
		_, err = router.GetRoute("production")
		assert.Error(t, err)
	})

	t.Run("PriorityOrder", func(t *testing.T) {
		router := NewDatabaseRouter(logger)
		
		// Add routes in specific order: exact > pattern > wildcard
		router.AddRoute("*", &RouteConfig{DatabaseName: "*", Label: "wildcard"})
		router.AddRoute("test_*", &RouteConfig{DatabaseName: "test_*", Label: "test_prefix"})
		router.AddRoute("test_db", &RouteConfig{DatabaseName: "test_db", Label: "exact"})
		
		// Exact match should win
		route, err := router.GetRoute("test_db")
		assert.NoError(t, err)
		assert.Equal(t, "exact", route.Label)
		
		// Pattern match should win over wildcard
		route, err = router.GetRoute("test_analytics")
		assert.NoError(t, err)
		assert.Equal(t, "test_prefix", route.Label)
		
		// Wildcard should be last resort
		route, err = router.GetRoute("production")
		assert.NoError(t, err)
		assert.Equal(t, "wildcard", route.Label)
	})

	t.Run("RemoveRoute", func(t *testing.T) {
		router := NewDatabaseRouter(logger)
		
		// Add routes
		router.AddRoute("mydb", &RouteConfig{DatabaseName: "mydb", Label: "exact"})
		router.AddRoute("*", &RouteConfig{DatabaseName: "*", Label: "wildcard"})
		
		// Remove exact route
		router.RemoveRoute("mydb")
		
		// Should now use wildcard
		route, err := router.GetRoute("mydb")
		assert.NoError(t, err)
		assert.Equal(t, "wildcard", route.Label)
		
		// Remove wildcard
		router.RemoveRoute("*")
		
		// Should now fail
		_, err = router.GetRoute("mydb")
		assert.Error(t, err)
	})

	t.Run("GetAllRoutes", func(t *testing.T) {
		router := NewDatabaseRouter(logger)
		
		// Add various routes
		router.AddRoute("db1", &RouteConfig{DatabaseName: "db1", Label: "exact1"})
		router.AddRoute("db2", &RouteConfig{DatabaseName: "db2", Label: "exact2"})
		router.AddRoute("test_*", &RouteConfig{DatabaseName: "test_*", Label: "test_prefix"})
		router.AddRoute("*_prod", &RouteConfig{DatabaseName: "*_prod", Label: "prod_suffix"})
		router.AddRoute("*", &RouteConfig{DatabaseName: "*", Label: "wildcard"})
		
		routes := router.GetAllRoutes()
		
		// Check all routes are present
		assert.Len(t, routes, 5)
		assert.Equal(t, "exact1", routes["db1"].Label)
		assert.Equal(t, "exact2", routes["db2"].Label)
		assert.Equal(t, "test_prefix", routes["test_*"].Label)
		assert.Equal(t, "prod_suffix", routes["*_prod"].Label)
		assert.Equal(t, "wildcard", routes["*"].Label)
	})
}

// TestExtractDatabaseName tests are simplified since we can't access internal types
// In a real implementation, we would need to either:
// 1. Export the operation types from the mongo package
// 2. Create helper functions in the mongo package
// 3. Use integration tests with actual MongoDB wire protocol messages