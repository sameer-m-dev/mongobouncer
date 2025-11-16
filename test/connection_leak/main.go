package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Test connection leak prevention features
func main() {
	uri := "mongodb://localhost:27017/mongobouncer_test"

	fmt.Println("=========================================")
	fmt.Println("MongoBouncer Connection Leak Test Suite")
	fmt.Println("=========================================")
	fmt.Println()

	// Test 1: Basic connection
	fmt.Println("Test 1: Basic connection")
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Disconnect(context.Background())

	db := client.Database("mongobouncer_test")
	collection := db.Collection("test")

	_, err = collection.InsertOne(context.Background(), bson.M{"test": 1})
	if err != nil {
		log.Fatalf("Failed to insert: %v", err)
	}
	fmt.Println("✓ Basic connection works")
	fmt.Println()

	// Test 2: Transaction and disconnect (simulate abandoned transaction)
	fmt.Println("Test 2: Transaction with client disconnect")
	fmt.Println("Starting transaction...")

	session, err := client.StartSession()
	if err != nil {
		log.Fatalf("Failed to start session: %v", err)
	}
	defer session.EndSession(context.Background())

	err = mongo.WithSession(context.Background(), session, func(sc mongo.SessionContext) error {
		err := session.StartTransaction()
		if err != nil {
			return err
		}

		// Insert a document in transaction
		_, err = collection.InsertOne(sc, bson.M{"txn_test": 1})
		if err != nil {
			return err
		}

		fmt.Println("  Transaction started, document inserted")
		fmt.Println("  Simulating client disconnect (not committing)...")

		// Don't commit - simulate abandoned transaction
		// The cleanup should handle this after timeout
		return nil
	})

	if err != nil {
		log.Printf("Transaction error (expected): %v", err)
	}

	// Disconnect client without committing
	client.Disconnect(context.Background())
	fmt.Println("✓ Client disconnected (transaction should be cleaned up)")
	fmt.Println()

	// Test 3: Multiple concurrent connections
	fmt.Println("Test 3: Concurrent connections")
	clients := make([]*mongo.Client, 10)
	for i := 0; i < 10; i++ {
		client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(uri))
		if err != nil {
			log.Printf("Failed to create client %d: %v", i, err)
			continue
		}
		clients[i] = client
	}

	success := 0
	for i, client := range clients {
		if client == nil {
			continue
		}
		db := client.Database("mongobouncer_test")
		collection := db.Collection("concurrent_test")
		_, err := collection.InsertOne(context.Background(), bson.M{"index": i})
		if err == nil {
			success++
		}
		client.Disconnect(context.Background())
	}

	fmt.Printf("✓ %d/10 concurrent connections succeeded\n", success)
	fmt.Println()

	// Test 4: Check metrics
	fmt.Println("Test 4: Checking metrics endpoint")
	resp, err := http.Get("http://localhost:9090/metrics")
	if err != nil {
		log.Printf("Failed to fetch metrics: %v", err)
	} else {
		defer resp.Body.Close()
		fmt.Println("✓ Metrics endpoint is accessible")
	}
	fmt.Println()

	// Test 5: Long-running operations
	fmt.Println("Test 5: Long-running operations")
	client2, err := mongo.Connect(context.Background(), options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client2.Disconnect(context.Background())

	db2 := client2.Database("mongobouncer_test")
	collection2 := db2.Collection("stress_test")

	for i := 0; i < 100; i++ {
		_, err := collection2.InsertOne(context.Background(), bson.M{
			"index":     i,
			"timestamp": time.Now(),
		})
		if err != nil {
			log.Printf("Failed to insert %d: %v", i, err)
		}
		if i%10 == 0 {
			fmt.Printf("  Inserted %d documents...\n", i)
		}
	}
	fmt.Println("✓ 100 operations completed")
	fmt.Println()

	fmt.Println("=========================================")
	fmt.Println("All tests completed!")
	fmt.Println("=========================================")
	fmt.Println()
	fmt.Println("Check metrics at: http://localhost:9090/metrics")
	fmt.Println("Look for:")
	fmt.Println("  - pinned_connections")
	fmt.Println("  - pool_active_connections")
	fmt.Println("  - abandoned_transactions_cleaned")
	fmt.Println("  - connection_refresh_success")
}
