package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	mongoURL   = "mongodb://localhost:27017/app_prod_rs8?retryWrites=true&appName=app_prod_rs8:appuser_rs8:apppass_rs8_123"
	database   = "app_prod_rs8"
	collection = "test_data"

	// High frequency settings for better Grafana visualization
	numWorkers          = 8
	cursorBatchSize     = 50
	queryInterval       = 500 * time.Millisecond // Very frequent queries
	insertInterval      = 1 * time.Second
	transactionInterval = 3 * time.Second
)

func main() {
	fmt.Println("MongoBouncer High-Frequency Cursor Metrics Generator")
	fmt.Println("===================================================")
	fmt.Printf("MongoDB URL: %s\n", mongoURL)
	fmt.Printf("Database: %s\n", database)
	fmt.Printf("Collection: %s\n", collection)
	fmt.Printf("Workers: %d\n", numWorkers)
	fmt.Printf("Query interval: %v\n", queryInterval)
	fmt.Printf("Insert interval: %v\n", insertInterval)
	fmt.Printf("Transaction interval: %v\n", transactionInterval)
	fmt.Println()

	// Connect to MongoDB
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURL))
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer client.Disconnect(context.Background())

	// Test connection
	err = client.Ping(context.Background(), nil)
	if err != nil {
		log.Fatalf("Failed to ping MongoDB: %v", err)
	}

	db := client.Database(database)
	coll := db.Collection(collection)

	// Create indexes for performance
	_, err = coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{Keys: bson.D{{Key: "timestamp", Value: 1}}},
		{Keys: bson.D{{Key: "worker_id", Value: 1}}},
		{Keys: bson.D{{Key: "category", Value: 1}}},
		{Keys: bson.D{{Key: "value", Value: 1}}},
	})
	if err != nil {
		log.Printf("Warning: Failed to create indexes: %v", err)
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nReceived shutdown signal, stopping...")
		cancel()
	}()

	// Populate initial data
	fmt.Println("Populating database with initial data...")
	populateInitialData(ctx, coll)

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			runHighFrequencyWorker(ctx, client, db, coll, workerID)
		}(i)
	}

	fmt.Println("All workers started. Generating high-frequency cursor metrics...")
	fmt.Println("This will create lots of cursor activity for Grafana visualization.")
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

	// Wait for workers
	wg.Wait()
	fmt.Println("All workers stopped.")
}

func populateInitialData(ctx context.Context, coll *mongo.Collection) {
	// Insert a large batch of documents for cursor operations
	docs := make([]interface{}, 2000)
	now := time.Now()

	for i := 0; i < 2000; i++ {
		docs[i] = bson.M{
			"worker_id": rand.Intn(10),
			"timestamp": now.Add(time.Duration(i) * time.Second),
			"category":  fmt.Sprintf("category_%d", rand.Intn(15)),
			"value":     rand.Float64() * 1000,
			"text":      generateRandomText(200),
			"metadata": bson.M{
				"batch": "initial",
				"index": i,
			},
		}
	}

	_, err := coll.InsertMany(ctx, docs)
	if err != nil {
		log.Printf("Failed to populate initial data: %v", err)
	} else {
		fmt.Printf("Inserted %d initial documents\n", 2000)
	}
}

func runHighFrequencyWorker(ctx context.Context, client *mongo.Client, db *mongo.Database, coll *mongo.Collection, workerID int) {
	fmt.Printf("High-frequency worker %d started\n", workerID)

	// Start different operation types
	go runFrequentCursorQueries(ctx, coll, workerID)
	go runFrequentInserts(ctx, coll, workerID)
	go runFrequentTransactions(ctx, client, coll, workerID)

	// Wait for context cancellation
	<-ctx.Done()
	fmt.Printf("High-frequency worker %d stopped\n", workerID)
}

func runFrequentCursorQueries(ctx context.Context, coll *mongo.Collection, workerID int) {
	ticker := time.NewTicker(queryInterval)
	defer ticker.Stop()

	queryTypes := []string{"find", "aggregation", "sorted", "paginated"}
	queryCount := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			queryType := queryTypes[queryCount%len(queryTypes)]
			queryCount++

			switch queryType {
			case "find":
				runFrequentFindQuery(ctx, coll, workerID)
			case "aggregation":
				runFrequentAggregationQuery(ctx, coll, workerID)
			case "sorted":
				runFrequentSortedQuery(ctx, coll, workerID)
			case "paginated":
				runFrequentPaginatedQuery(ctx, coll, workerID)
			}
		}
	}
}

func runFrequentFindQuery(ctx context.Context, coll *mongo.Collection, workerID int) {
	filter := bson.M{
		"worker_id": bson.M{"$gte": 0},
		"timestamp": bson.M{
			"$gte": time.Now().Add(-30 * time.Minute),
		},
	}

	opts := options.Find().
		SetBatchSize(cursorBatchSize).
		SetMaxTime(5 * time.Second).
		SetNoCursorTimeout(false)

	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		log.Printf("Worker %d: Frequent find query failed: %v", workerID, err)
		return
	}
	defer cursor.Close(ctx)

	count := 0
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			break
		}
		count++

		// Process every 10th document
		if count%10 == 0 {
			time.Sleep(1 * time.Millisecond)
		}
	}

	if err := cursor.Err(); err != nil {
		log.Printf("Worker %d: Frequent find cursor error: %v", workerID, err)
		return
	}

	fmt.Printf("Worker %d: Frequent find processed %d docs\n", workerID, count)
}

func runFrequentAggregationQuery(ctx context.Context, coll *mongo.Collection, workerID int) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"category": bson.M{"$regex": fmt.Sprintf("category_%d", workerID%5)}}}},
		{{Key: "$group", Value: bson.M{
			"_id":       "$category",
			"count":     bson.M{"$sum": 1},
			"avg_value": bson.M{"$avg": "$value"},
		}}},
		{{Key: "$sort", Value: bson.M{"count": -1}}},
		{{Key: "$limit", Value: 20}},
	}

	opts := options.Aggregate().
		SetBatchSize(cursorBatchSize / 2).
		SetMaxTime(3 * time.Second)

	cursor, err := coll.Aggregate(ctx, pipeline, opts)
	if err != nil {
		log.Printf("Worker %d: Frequent aggregation failed: %v", workerID, err)
		return
	}
	defer cursor.Close(ctx)

	count := 0
	for cursor.Next(ctx) {
		var result bson.M
		if err := cursor.Decode(&result); err != nil {
			break
		}
		count++
	}

	if err := cursor.Err(); err != nil {
		log.Printf("Worker %d: Frequent aggregation cursor error: %v", workerID, err)
		return
	}

	fmt.Printf("Worker %d: Frequent aggregation processed %d results\n", workerID, count)
}

func runFrequentSortedQuery(ctx context.Context, coll *mongo.Collection, workerID int) {
	filter := bson.M{
		"value": bson.M{"$gte": float64(workerID * 50)},
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: -1}}).
		SetLimit(100).
		SetBatchSize(cursorBatchSize / 2).
		SetMaxTime(3 * time.Second)

	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		log.Printf("Worker %d: Frequent sorted query failed: %v", workerID, err)
		return
	}
	defer cursor.Close(ctx)

	count := 0
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			break
		}
		count++
	}

	if err := cursor.Err(); err != nil {
		log.Printf("Worker %d: Frequent sorted cursor error: %v", workerID, err)
		return
	}

	fmt.Printf("Worker %d: Frequent sorted processed %d docs\n", workerID, count)
}

func runFrequentPaginatedQuery(ctx context.Context, coll *mongo.Collection, workerID int) {
	skip := (workerID * 10) % 500
	limit := 50

	filter := bson.M{
		"timestamp": bson.M{
			"$gte": time.Now().Add(-15 * time.Minute),
		},
	}

	opts := options.Find().
		SetSkip(int64(skip)).
		SetLimit(int64(limit)).
		SetBatchSize(cursorBatchSize / 2).
		SetMaxTime(2 * time.Second)

	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		log.Printf("Worker %d: Frequent paginated query failed: %v", workerID, err)
		return
	}
	defer cursor.Close(ctx)

	count := 0
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			break
		}
		count++
	}

	if err := cursor.Err(); err != nil {
		log.Printf("Worker %d: Frequent paginated cursor error: %v", workerID, err)
		return
	}

	fmt.Printf("Worker %d: Frequent paginated processed %d docs\n", workerID, count)
}

func runFrequentInserts(ctx context.Context, coll *mongo.Collection, workerID int) {
	ticker := time.NewTicker(insertInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Insert small batch frequently
			docs := make([]interface{}, 20)
			now := time.Now()

			for i := 0; i < 20; i++ {
				docs[i] = bson.M{
					"worker_id": workerID,
					"timestamp": now.Add(time.Duration(i) * time.Millisecond),
					"category":  fmt.Sprintf("category_%d", rand.Intn(15)),
					"value":     rand.Float64() * 1000,
					"text":      generateRandomText(100),
					"batch":     fmt.Sprintf("freq_batch_%d_%d", workerID, now.Unix()),
				}
			}

			_, err := coll.InsertMany(ctx, docs)
			if err != nil {
				log.Printf("Worker %d: Frequent insert failed: %v", workerID, err)
			} else {
				fmt.Printf("Worker %d: Frequent insert 20 docs\n", workerID)
			}
		}
	}
}

func runFrequentTransactions(ctx context.Context, client *mongo.Client, coll *mongo.Collection, workerID int) {
	ticker := time.NewTicker(transactionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			session, err := client.StartSession()
			if err != nil {
				log.Printf("Worker %d: Failed to start session: %v", workerID, err)
				continue
			}

			// Run transaction
			_, err = session.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
				// Insert document
				doc := bson.M{
					"worker_id":   workerID,
					"timestamp":   time.Now(),
					"transaction": true,
					"txn_id":      fmt.Sprintf("freq_txn_%d_%d", workerID, time.Now().Unix()),
					"value":       rand.Float64() * 1000,
					"category":    fmt.Sprintf("txn_category_%d", workerID%5),
				}

				_, err := coll.InsertOne(sc, doc)
				if err != nil {
					return nil, err
				}

				// Update some documents
				filter := bson.M{"worker_id": workerID}
				update := bson.M{"$set": bson.M{
					"last_frequent_txn": time.Now(),
					"txn_worker":        workerID,
				}}

				_, err = coll.UpdateMany(sc, filter, update)
				if err != nil {
					return nil, err
				}

				// Randomly abort some transactions (20% abort rate for more variety)
				if rand.Float64() < 0.2 {
					return nil, fmt.Errorf("simulated frequent transaction abort for worker %d", workerID)
				}

				return nil, nil
			})

			session.EndSession(ctx)

			if err != nil {
				log.Printf("Worker %d: Frequent transaction aborted: %v", workerID, err)
			} else {
				fmt.Printf("Worker %d: Frequent transaction committed\n", workerID)
			}
		}
	}
}

func generateRandomText(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 "
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[rand.Intn(len(charset))]
	}
	return string(result)
}
