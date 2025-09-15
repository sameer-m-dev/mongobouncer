package util

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// DistributionOperation represents a batched distribution operation
type DistributionOperation struct {
	Name  string
	Value float64
	Tags  []string
	Rate  float64
}

// BatchedMetricsClient provides batched metrics collection to reduce overhead
type BatchedMetricsClient struct {
	client        *MetricsClient
	logger        *zap.Logger
	batchSize     int
	flushInterval time.Duration

	// Batched operations
	timingBatch       []TimingOperation
	counterBatch      []CounterOperation
	gaugeBatch        []GaugeOperation
	distributionBatch []DistributionOperation

	// Mutex for batch operations
	mutex sync.Mutex

	// Background flush goroutine
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// TimingOperation represents a batched timing operation
type TimingOperation struct {
	Name     string
	Duration time.Duration
	Tags     []string
	Rate     float64
}

// CounterOperation represents a batched counter operation
type CounterOperation struct {
	Name string
	Tags []string
	Rate float64
}

// GaugeOperation represents a batched gauge operation
type GaugeOperation struct {
	Name  string
	Value float64
	Tags  []string
	Rate  float64
}

// NewBatchedMetricsClient creates a new batched metrics client
func NewBatchedMetricsClient(client *MetricsClient, logger *zap.Logger, batchSize int, flushInterval time.Duration) *BatchedMetricsClient {
	ctx, cancel := context.WithCancel(context.Background())

	bmc := &BatchedMetricsClient{
		client:            client,
		logger:            logger,
		batchSize:         batchSize,
		flushInterval:     flushInterval,
		timingBatch:       make([]TimingOperation, 0, batchSize),
		counterBatch:      make([]CounterOperation, 0, batchSize),
		gaugeBatch:        make([]GaugeOperation, 0, batchSize),
		distributionBatch: make([]DistributionOperation, 0, batchSize),
		ctx:               ctx,
		cancel:            cancel,
	}

	// Start background flush goroutine
	bmc.wg.Add(1)
	go bmc.flushLoop()

	return bmc
}

// Timing records a timing metric in batch
func (bmc *BatchedMetricsClient) Timing(name string, duration time.Duration, tags []string, rate float64) error {
	bmc.mutex.Lock()
	defer bmc.mutex.Unlock()

	bmc.timingBatch = append(bmc.timingBatch, TimingOperation{
		Name:     name,
		Duration: duration,
		Tags:     tags,
		Rate:     rate,
	})

	// Flush if batch is full
	if len(bmc.timingBatch) >= bmc.batchSize {
		bmc.flushTimingBatch()
	}

	return nil
}

// Incr records a counter metric in batch
func (bmc *BatchedMetricsClient) Incr(name string, tags []string, rate float64) error {
	bmc.mutex.Lock()
	defer bmc.mutex.Unlock()

	bmc.counterBatch = append(bmc.counterBatch, CounterOperation{
		Name: name,
		Tags: tags,
		Rate: rate,
	})

	// Flush if batch is full
	if len(bmc.counterBatch) >= bmc.batchSize {
		bmc.flushCounterBatch()
	}

	return nil
}

// Gauge records a gauge metric in batch
func (bmc *BatchedMetricsClient) Gauge(name string, value float64, tags []string, rate float64) error {
	bmc.mutex.Lock()
	defer bmc.mutex.Unlock()

	bmc.gaugeBatch = append(bmc.gaugeBatch, GaugeOperation{
		Name:  name,
		Value: value,
		Tags:  tags,
		Rate:  rate,
	})

	// Flush if batch is full
	if len(bmc.gaugeBatch) >= bmc.batchSize {
		bmc.flushGaugeBatch()
	}

	return nil
}

// Distribution records a distribution metric in batch
func (bmc *BatchedMetricsClient) Distribution(name string, value float64, tags []string, rate float64) error {
	bmc.mutex.Lock()
	defer bmc.mutex.Unlock()

	bmc.distributionBatch = append(bmc.distributionBatch, DistributionOperation{
		Name:  name,
		Value: value,
		Tags:  tags,
		Rate:  rate,
	})

	// Flush if batch is full
	if len(bmc.distributionBatch) >= bmc.batchSize {
		bmc.flushDistributionBatch()
	}

	return nil
}

// BackgroundGauge provides background gauge callbacks (not batched)
func (bmc *BatchedMetricsClient) BackgroundGauge(name string, tags []string) (increment, decrement BackgroundGaugeCallback) {
	return bmc.client.BackgroundGauge(name, tags)
}

// Flush forces immediate flush of all batches
func (bmc *BatchedMetricsClient) Flush() error {
	bmc.mutex.Lock()
	defer bmc.mutex.Unlock()

	bmc.flushAllBatches()
	return nil
}

// Close shuts down the batched metrics client
func (bmc *BatchedMetricsClient) Close() error {
	// Cancel context to stop flush loop
	bmc.cancel()

	// Wait for flush loop to finish
	bmc.wg.Wait()

	// Final flush
	bmc.Flush()

	return bmc.client.Close()
}

// Shutdown gracefully shuts down the batched metrics client
func (bmc *BatchedMetricsClient) Shutdown(ctx context.Context) error {
	// Cancel the background flush goroutine
	bmc.cancel()

	// Wait for the flush goroutine to finish
	done := make(chan struct{})
	go func() {
		bmc.wg.Wait()
		close(done)
	}()

	// Wait for either context cancellation or flush completion
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// flushLoop runs in background to periodically flush batches
func (bmc *BatchedMetricsClient) flushLoop() {
	defer bmc.wg.Done()

	ticker := time.NewTicker(bmc.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bmc.ctx.Done():
			return
		case <-ticker.C:
			bmc.Flush()
		}
	}
}

// flushTimingBatch flushes timing batch
func (bmc *BatchedMetricsClient) flushTimingBatch() {
	if len(bmc.timingBatch) == 0 {
		return
	}

	for _, op := range bmc.timingBatch {
		bmc.client.Timing(op.Name, op.Duration, op.Tags, op.Rate)
	}

	bmc.timingBatch = bmc.timingBatch[:0] // Reset slice
}

// flushCounterBatch flushes counter batch
func (bmc *BatchedMetricsClient) flushCounterBatch() {
	if len(bmc.counterBatch) == 0 {
		return
	}

	for _, op := range bmc.counterBatch {
		bmc.client.Incr(op.Name, op.Tags, op.Rate)
	}

	bmc.counterBatch = bmc.counterBatch[:0] // Reset slice
}

// flushGaugeBatch flushes gauge batch
func (bmc *BatchedMetricsClient) flushGaugeBatch() {
	if len(bmc.gaugeBatch) == 0 {
		return
	}

	for _, op := range bmc.gaugeBatch {
		bmc.client.Gauge(op.Name, op.Value, op.Tags, op.Rate)
	}

	bmc.gaugeBatch = bmc.gaugeBatch[:0] // Reset slice
}

// flushDistributionBatch flushes distribution batch
func (bmc *BatchedMetricsClient) flushDistributionBatch() {
	if len(bmc.distributionBatch) == 0 {
		return
	}

	for _, op := range bmc.distributionBatch {
		bmc.client.Distribution(op.Name, op.Value, op.Tags, op.Rate)
	}

	bmc.distributionBatch = bmc.distributionBatch[:0] // Reset slice
}

// flushAllBatches flushes all batches
func (bmc *BatchedMetricsClient) flushAllBatches() {
	bmc.flushTimingBatch()
	bmc.flushCounterBatch()
	bmc.flushGaugeBatch()
	bmc.flushDistributionBatch()
}
