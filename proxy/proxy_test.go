package proxy

import (
	"context"
	"fmt"
	"os"
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
	assert.Nil(t, err)

	ash := Trainer{"Ash", 10, "Pallet Town"}
	misty := Trainer{"Misty", 10, "Cerulean City"}
	brock := Trainer{"Brock", 15, "Pewter City"}

	_, err = collection.InsertOne(ctx, ash)
	assert.Nil(t, err)

	_, err = collection.InsertMany(ctx, []interface{}{misty, brock})
	assert.Nil(t, err)

	filter := bson.D{{Key: "name", Value: "Ash"}}
	update := bson.D{
		{Key: "$inc", Value: bson.D{
			{Key: "age", Value: 1},
		}},
	}
	updateResult, err := collection.UpdateOne(ctx, filter, update)
	assert.Nil(t, err)
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
	assert.Nil(t, err)

	ash := Trainer{"Ash", 10, "Pallet Town"}
	_, err = unackCollection.InsertOne(ctx, ash)
	assert.Equal(t, mongo.ErrUnacknowledgedWrite, err) // driver returns a special error value for w=0 writes

	// Insert a document using the setup collection and ensure document count is 2. Doing this ensures that the proxy
	// did not crash while processing the unacknowledged write.
	_, err = setupCollection.InsertOne(ctx, ash)
	assert.Nil(t, err)

	count, err := setupCollection.CountDocuments(ctx, bson.D{})
	assert.Nil(t, err)
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
	lookup := func(address string) *mongob.Mongo {
		return upstreams[address]
	}

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

		proxy, err := NewProxy(zap.L(), metrics, "label", "tcp4", address, false, lookup, nil, nil)
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

// TestHostBasedAdminRouting tests the host-based admin routing functionality
func TestHostBasedAdminRouting(t *testing.T) {
	if !isMongoDBAvailable() {
		t.Skip("MongoDB not available for testing")
	}

	logger := zap.NewNop()
	databaseRouter := NewDatabaseRouter(logger)

	// Create test route configurations
	testRoute1 := &RouteConfig{
		DatabaseName:     "mongobouncer",
		ConnectionString: "mongodb://localhost:27017/mongobouncer",
		MongoClient:      nil, // We don't need actual clients for this test
	}

	testRoute2 := &RouteConfig{
		DatabaseName:     "analytics",
		ConnectionString: "mongodb://localhost:27018/analytics",
		MongoClient:      nil,
	}

	// Add routes to the router
	databaseRouter.AddRoute("mongobouncer", testRoute1)
	databaseRouter.AddRoute("analytics", testRoute2)

	// Create a mock connection for testing
	c := &connection{
		log:              logger,
		databaseRouter:   databaseRouter,
		intendedDatabase: "mongobouncer", // Client wants to connect to mongobouncer DB
	}

	// Test host extraction
	host, port := c.extractHostFromRoute(testRoute1)
	assert.Equal(t, "localhost", host)
	assert.Equal(t, "27017", port)

	host, port = c.extractHostFromRoute(testRoute2)
	assert.Equal(t, "localhost", host)
	assert.Equal(t, "27018", port)

	// Test admin route finding
	adminRoute := c.findAdminRouteForTarget(testRoute1)
	// Since we don't have a matching admin route configured, this should return empty
	// In a real scenario, you'd configure an admin route like "*_27017" for localhost:27017
	assert.Equal(t, "", adminRoute)

	// Test determineTargetDatabase with admin operation
	targetDB := c.determineTargetDatabase("admin", nil)
	// Should fall back to catch-all or admin since no matching admin route found
	assert.True(t, targetDB == "*" || targetDB == "admin")

	// Test determineTargetDatabase with non-admin operation
	c.intendedDatabase = "" // Reset
	targetDB = c.determineTargetDatabase("mongobouncer", nil)
	assert.Equal(t, "mongobouncer", targetDB)
	assert.Equal(t, "mongobouncer", c.intendedDatabase)

	// Now test admin routing with intended database set
	targetDB = c.determineTargetDatabase("admin", nil)
	// Should try to route admin to same host as mongobouncer
	assert.True(t, targetDB == "*" || targetDB == "admin")
}

// TestExtractHostAndPort tests the host and port extraction functionality
func TestExtractHostAndPort(t *testing.T) {
	logger := zap.NewNop()
	c := &connection{log: logger}

	// Test IPv4 format
	host, port := c.extractHostAndPort("localhost:27017")
	assert.Equal(t, "localhost", host)
	assert.Equal(t, "27017", port)

	// Test IPv6 format
	host, port = c.extractHostAndPort("[::1]:27017")
	assert.Equal(t, "::1", host)
	assert.Equal(t, "27017", port)

	// Test default port
	host, port = c.extractHostAndPort("localhost")
	assert.Equal(t, "localhost", host)
	assert.Equal(t, "27017", port)

	// Test IPv6 without port
	host, port = c.extractHostAndPort("[::1]")
	assert.Equal(t, "::1", host)
	assert.Equal(t, "27017", port)
}
