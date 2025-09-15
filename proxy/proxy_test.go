package proxy

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sameer-m-dev/mongobouncer/util"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"go.uber.org/zap"

	mongob "github.com/sameer-m-dev/mongobouncer/mongo"
)

var (
	ctx       = context.Background()
	proxyPort = 27020
)

// isMongoDBAvailable checks if MongoDB is available for testing
func isMongoDBAvailable() bool {
	uri := "mongodb://localhost:27017/test"
	if os.Getenv("CI") == "true" {
		uri = "mongodb://mongo1:27017/test"
	}

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return false
	}
	defer client.Disconnect(ctx)

	// Try to ping MongoDB
	err = client.Ping(ctx, nil)
	return err == nil
}

type Trainer struct {
	Name string
	Age  int
	City string
}

func TestProxy(t *testing.T) {
	// Check if MongoDB is available first
	if !isMongoDBAvailable() {
		t.Skip("MongoDB not available")
	}

	proxy := setupProxy(t)

	go func() {
		err := proxy.Run()
		if err != nil {
			t.Logf("Proxy run error (expected if MongoDB not available): %v", err)
		}
	}()

	client := setupClient(t, "localhost", proxyPort)
	collection := client.Database("test").Collection("test_proxy")
	_, err := collection.DeleteMany(ctx, bson.D{{}})
	if err != nil {
		// If authentication is required, skip this test
		if strings.Contains(err.Error(), "authentication") {
			t.Skipf("MongoDB requires authentication: %v", err)
		}
		assert.Nil(t, err)
	}

	ash := Trainer{"Ash", 10, "Pallet Town"}
	misty := Trainer{"Misty", 10, "Cerulean City"}
	brock := Trainer{"Brock", 15, "Pewter City"}

	_, err = collection.InsertOne(ctx, ash)
	if err != nil {
		if strings.Contains(err.Error(), "authentication") {
			t.Skipf("MongoDB requires authentication: %v", err)
		}
		assert.Nil(t, err)
	}

	_, err = collection.InsertMany(ctx, []interface{}{misty, brock})
	if err != nil {
		if strings.Contains(err.Error(), "authentication") {
			t.Skipf("MongoDB requires authentication: %v", err)
		}
		assert.Nil(t, err)
	}

	filter := bson.D{{Key: "name", Value: "Ash"}}
	update := bson.D{
		{Key: "$inc", Value: bson.D{
			{Key: "age", Value: 1},
		}},
	}
	updateResult, err := collection.UpdateOne(ctx, filter, update)
	if err != nil {
		if strings.Contains(err.Error(), "authentication") {
			t.Skipf("MongoDB requires authentication: %v", err)
		}
		assert.Nil(t, err)
	}
	assert.Equal(t, int64(1), updateResult.MatchedCount)
	assert.Equal(t, int64(1), updateResult.ModifiedCount)

	var result Trainer
	err = collection.FindOne(ctx, filter).Decode(&result)
	assert.Nil(t, err)
	assert.Equal(t, "Pallet Town", result.City)

	var results []Trainer
	cur, err := collection.Find(ctx, bson.D{}, options.Find().SetLimit(2).SetBatchSize(1))
	assert.Nil(t, err)
	err = cur.All(ctx, &results)
	assert.Nil(t, err)
	assert.Equal(t, "Pallet Town", results[0].City)
	assert.Equal(t, "Cerulean City", results[1].City)

	deleteResult, err := collection.DeleteMany(ctx, bson.D{{}})
	assert.Nil(t, err)
	assert.Equal(t, int64(3), deleteResult.DeletedCount)

	err = client.Disconnect(ctx)
	assert.Nil(t, err)

	proxy.Shutdown()
}

func TestProxyUnacknowledgedWrites(t *testing.T) {
	// Check if MongoDB is available first
	if !isMongoDBAvailable() {
		t.Skip("MongoDB not available")
	}

	proxy := setupProxy(t)
	defer proxy.Shutdown()

	go func() {
		err := proxy.Run()
		if err != nil {
			t.Logf("Proxy run error (expected if MongoDB not available): %v", err)
		}
	}()

	// Create a client with retryable writes disabled so the test will fail if the proxy crashes while processing the
	// unacknowledged write. If the proxy were to crash, it would close all connections and the next write would error
	// if retryable writes are disabled.
	clientOpts := options.Client().SetRetryWrites(false)
	client := setupClient(t, "localhost", proxyPort, clientOpts)
	defer func() {
		err := client.Disconnect(ctx)
		assert.Nil(t, err)
	}()

	// Create two *Collection instances: one for setup and basic operations and and one configured with an
	// unacknowledged write concern for testing.
	wc := writeconcern.Unacknowledged()
	setupCollection := client.Database("test").Collection("test_proxy_unacknowledged_writes")
	unackCollection, err := setupCollection.Clone(options.Collection().SetWriteConcern(wc))
	assert.Nil(t, err)

	// Setup by deleting all documents.
	_, err = setupCollection.DeleteMany(ctx, bson.D{})
	if err != nil {
		if strings.Contains(err.Error(), "authentication") {
			t.Skipf("MongoDB requires authentication: %v", err)
		}
		assert.Nil(t, err)
	}

	ash := Trainer{"Ash", 10, "Pallet Town"}
	_, err = unackCollection.InsertOne(ctx, ash)
	if err != nil {
		if strings.Contains(err.Error(), "authentication") {
			t.Skipf("MongoDB requires authentication: %v", err)
		}
		assert.Equal(t, mongo.ErrUnacknowledgedWrite, err) // driver returns a special error value for w=0 writes
	}

	// Insert a document using the setup collection and ensure document count is 2. Doing this ensures that the proxy
	// did not crash while processing the unacknowledged write.
	_, err = setupCollection.InsertOne(ctx, ash)
	if err != nil {
		if strings.Contains(err.Error(), "authentication") {
			t.Skipf("MongoDB requires authentication: %v", err)
		}
		assert.Nil(t, err)
	}

	count, err := setupCollection.CountDocuments(ctx, bson.D{})
	if err != nil {
		if strings.Contains(err.Error(), "authentication") {
			t.Skipf("MongoDB requires authentication: %v", err)
		}
		assert.Nil(t, err)
	}
	assert.Equal(t, int64(2), count)
}

func setupProxy(t *testing.T) *Proxy {
	t.Helper()

	return setupProxies(t, proxyPort, 1)[0]
}

func setupProxies(t *testing.T, startPort int, count int) []*Proxy {
	t.Helper()

	metrics, err := util.NewMetricsClient(zap.L(), "localhost:9090")
	assert.Nil(t, err)

	upstreams := make(map[string]*mongob.Mongo)

	var proxies []*Proxy
	for i := 0; i < count; i++ {
		port := startPort + i
		uri := fmt.Sprintf("mongodb://localhost:%d/test", 27017+port-proxyPort)
		if os.Getenv("CI") == "true" {
			uri = fmt.Sprintf("mongodb://mongo%d:27017/test", 1+port-proxyPort)
		}

		address := fmt.Sprintf(":%d", port)

		upstream, err := mongob.Connect(zap.L(), metrics, options.Client().ApplyURI(uri), false)
		if err != nil {
			// If MongoDB is not running, skip this test
			t.Skipf("MongoDB not available: %v", err)
		}
		upstreams[address] = upstream

		proxy, err := NewProxy(zap.L(), metrics, "label", "tcp4", address, false, nil, nil, false, false, util.MongoDBClientConfig{})
		assert.Nil(t, err)

		proxies = append(proxies, proxy)
	}

	return proxies
}

func setupClient(t *testing.T, host string, port int, clientOpts ...*options.ClientOptions) *mongo.Client {
	t.Helper()

	// Base options should only use ApplyURI. The full set should have the user-supplied options after uriOpts so they
	// will win out in the case of conflicts.
	proxyURI := fmt.Sprintf("mongodb://%s:%d/test", host, port)
	uriOpts := options.Client().ApplyURI(proxyURI)
	allClientOpts := append([]*options.ClientOptions{uriOpts}, clientOpts...)

	client, err := mongo.Connect(ctx, allClientOpts...)
	assert.Nil(t, err)

	// Call Ping with a low timeout to ensure the cluster is running and fail-fast if not.
	pingCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	err = client.Ping(pingCtx, nil)
	if err != nil {
		// Clean up in failure cases.
		_ = client.Disconnect(ctx)

		// Use t.Fatalf instead of assert because we want to fail fast if the cluster is down.
		t.Fatalf("error pinging cluster: %v", err)
	}

	return client
}

func TestIsWildcardOrRegexMatch(t *testing.T) {
	// Create a mock connection for testing with a logger
	logger, _ := zap.NewDevelopment()
	c := &connection{
		log: logger,
	}

	tests := []struct {
		name           string
		targetDatabase string
		routePattern   string
		expected       bool
	}{
		{
			name:           "exact match",
			targetDatabase: "test_db",
			routePattern:   "test_db",
			expected:       false,
		},
		{
			name:           "wildcard prefix",
			targetDatabase: "test_db",
			routePattern:   "test_*",
			expected:       true,
		},
		{
			name:           "wildcard suffix",
			targetDatabase: "test_db",
			routePattern:   "*_db",
			expected:       true,
		},
		{
			name:           "wildcard contains",
			targetDatabase: "test_staging_db",
			routePattern:   "*_staging_*",
			expected:       true,
		},
		{
			name:           "full wildcard",
			targetDatabase: "any_database",
			routePattern:   "*",
			expected:       true,
		},
		{
			name:           "pattern match by non-exact",
			targetDatabase: "test_db",
			routePattern:   "test_*",
			expected:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := &RouteConfig{
				DatabaseName: tt.routePattern,
			}

			result := c.isWildcardOrRegexMatch(tt.targetDatabase, route)
			if result != tt.expected {
				t.Errorf("isWildcardOrRegexMatch(%s, %s) = %v, expected %v",
					tt.targetDatabase, tt.routePattern, result, tt.expected)
			}
		})
	}
}

func TestSanitizeConnectionString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "connection string with credentials",
			input:    "mongodb://user:password@localhost:27017/db",
			expected: "mongodb://user:***@localhost:27017/db",
		},
		{
			name:     "connection string without credentials",
			input:    "mongodb://localhost:27017/db",
			expected: "mongodb://localhost:27017/db",
		},
		{
			name:     "connection string with user only",
			input:    "mongodb://user@localhost:27017/db",
			expected: "mongodb://user@localhost:27017/db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeConnectionString(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeConnectionString(%s) = %s, expected %s",
					tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractHostFromRoute(t *testing.T) {
	// Create a mock connection for testing with a logger
	logger, _ := zap.NewDevelopment()
	c := &connection{
		log: logger,
	}

	tests := []struct {
		name             string
		connectionString string
		expectedHost     string
		expectedPort     string
	}{
		{
			name:             "standard connection string",
			connectionString: "mongodb://localhost:27017/db",
			expectedHost:     "localhost",
			expectedPort:     "27017",
		},
		{
			name:             "connection string with credentials",
			connectionString: "mongodb://user:pass@localhost:27017/db",
			expectedHost:     "localhost",
			expectedPort:     "27017",
		},
		{
			name:             "connection string with IPv6",
			connectionString: "mongodb://[::1]:27017/db",
			expectedHost:     "::1",
			expectedPort:     "27017",
		},
		{
			name:             "connection string without port",
			connectionString: "mongodb://localhost/db",
			expectedHost:     "localhost",
			expectedPort:     "27017", // Default port
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := &RouteConfig{
				ConnectionString: tt.connectionString,
			}

			host, port := c.extractHostFromRoute(route)
			if host != tt.expectedHost {
				t.Errorf("extractHostFromRoute() host = %s, expected %s", host, tt.expectedHost)
			}
			if port != tt.expectedPort {
				t.Errorf("extractHostFromRoute() port = %s, expected %s", port, tt.expectedPort)
			}
		})
	}
}

// TestExtractHostAndPort tests the host and port extraction functionality
func TestExtractHostAndPort(t *testing.T) {
	// Create a mock connection for testing with a logger
	logger, _ := zap.NewDevelopment()
	c := &connection{
		log: logger,
	}

	tests := []struct {
		name         string
		hostPort     string
		expectedHost string
		expectedPort string
	}{
		{
			name:         "standard host:port",
			hostPort:     "localhost:27017",
			expectedHost: "localhost",
			expectedPort: "27017",
		},
		{
			name:         "IPv6 address",
			hostPort:     "[::1]:27017",
			expectedHost: "::1",
			expectedPort: "27017",
		},
		{
			name:         "host without port",
			hostPort:     "localhost",
			expectedHost: "localhost",
			expectedPort: "27017", // Default port
		},
		{
			name:         "IPv6 without port",
			hostPort:     "[::1]",
			expectedHost: "::1",
			expectedPort: "27017", // Default port
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port := c.extractHostAndPort(tt.hostPort)
			if host != tt.expectedHost {
				t.Errorf("extractHostAndPort() host = %s, expected %s", host, tt.expectedHost)
			}
			if port != tt.expectedPort {
				t.Errorf("extractHostAndPort() port = %s, expected %s", port, tt.expectedPort)
			}
		})
	}
}
