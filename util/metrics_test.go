package util

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewMetricsClient(t *testing.T) {
	logger := zap.NewNop()

	// Test with valid address
	client, err := NewMetricsClient(logger, "localhost:19090")
	require.NoError(t, err)
	assert.NotNil(t, client)

	// Clean up
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = client.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestMetricsInterface(t *testing.T) {
	logger := zap.NewNop()
	client, err := NewMetricsClient(logger, "localhost:19091")
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Shutdown(ctx)
	}()

	// Test Timing metric
	err = client.Timing("handle_message", 100*time.Millisecond, []string{"success:true"}, 1.0)
	assert.NoError(t, err)

	// Test Incr metric
	err = client.Incr("connection_opened", []string{"cluster:test"}, 1.0)
	assert.NoError(t, err)

	// Test Gauge metric
	err = client.Gauge("open_connections", 42.0, []string{"cluster:test"}, 1.0)
	assert.NoError(t, err)

	// Test Distribution metric
	err = client.Distribution("request_size", 1024.0, []string{"cluster:test"}, 1.0)
	assert.NoError(t, err)

	// Test Flush (should be no-op)
	err = client.Flush()
	assert.NoError(t, err)
}

func TestBackgroundGauge(t *testing.T) {
	logger := zap.NewNop()
	client, err := NewMetricsClient(logger, "localhost:19092")
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Shutdown(ctx)
	}()

	increment, decrement := client.BackgroundGauge("test_connections", []string{"cluster:test"})

	// Test increment and decrement callbacks
	increment("connection_opened", []string{"cluster:test"})
	decrement("connection_closed", []string{"cluster:test"})

	// Give some time for background processing
	time.Sleep(100 * time.Millisecond)

	// Should not panic and should be callable
	assert.NotNil(t, increment)
	assert.NotNil(t, decrement)
}

func TestParseTagsToLabels(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		expected []string
	}{
		{
			name:     "Empty tags",
			tags:     []string{},
			expected: []string{"default"},
		},
		{
			name:     "Key-value tags",
			tags:     []string{"cluster:prod", "success:true"},
			expected: []string{"prod", "true"},
		},
		{
			name:     "Simple tags",
			tags:     []string{"test", "cluster"},
			expected: []string{"test", "cluster"},
		},
		{
			name:     "Mixed tags",
			tags:     []string{"cluster:prod", "simple", "success:false"},
			expected: []string{"prod", "simple", "false"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTagsToLabels(tt.tags)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMetricsHTTPEndpoint(t *testing.T) {
	logger := zap.NewNop()
	client, err := NewMetricsClient(logger, "localhost:19093")
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Shutdown(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test metrics endpoint
	resp, err := http.Get("http://localhost:19093/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")

	// Test health endpoint
	resp, err = http.Get("http://localhost:19093/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestConcurrentMetrics(t *testing.T) {
	logger := zap.NewNop()
	client, err := NewMetricsClient(logger, "localhost:19094")
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Shutdown(ctx)
	}()

	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numOperations; j++ {
				// Mix different metric operations
				_ = client.Incr("test_counter", []string{fmt.Sprintf("worker:%d", id)}, 1.0)
				_ = client.Timing("test_timing", time.Duration(j)*time.Millisecond, []string{fmt.Sprintf("worker:%d", id)}, 1.0)
				_ = client.Gauge("test_gauge", float64(j), []string{fmt.Sprintf("worker:%d", id)}, 1.0)
			}
		}(i)
	}

	wg.Wait()

	// If we get here without panicking, concurrent access works
	assert.True(t, true)
}
