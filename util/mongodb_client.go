package util

import (
	"time"

	"go.mongodb.org/mongo-driver/mongo/description"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

// ApplyMongoDBClientSettings applies MongoDB client pool settings from config
// This function is shared between config and proxy packages to avoid duplication
func ApplyMongoDBClientSettings(
	opts *options.ClientOptions,
	dbName string,
	defaults MongoDBClientConfig,
	dbOverrides *MongoDBClientConfig,
	logger *zap.Logger,
) *options.ClientOptions {
	// Start with defaults
	maxPoolSize := defaults.MaxPoolSize
	minPoolSize := defaults.MinPoolSize
	maxConnIdleTime := defaults.MaxConnIdleTime
	serverSelectionTimeout := defaults.ServerSelectionTimeout
	connectTimeout := defaults.ConnectTimeout
	socketTimeout := defaults.SocketTimeout
	heartbeatInterval := defaults.HeartbeatInterval

	// Apply database-level overrides if provided
	overrides := make(map[string]interface{})
	if dbOverrides != nil {
		if dbOverrides.MaxPoolSize > 0 {
			maxPoolSize = dbOverrides.MaxPoolSize
			overrides["max_pool_size"] = maxPoolSize
		}
		if dbOverrides.MinPoolSize > 0 {
			minPoolSize = dbOverrides.MinPoolSize
			overrides["min_pool_size"] = minPoolSize
		}
		if dbOverrides.MaxConnIdleTime > 0 {
			maxConnIdleTime = dbOverrides.MaxConnIdleTime
			overrides["max_conn_idle_time"] = maxConnIdleTime
		}
		if dbOverrides.ServerSelectionTimeout > 0 {
			serverSelectionTimeout = dbOverrides.ServerSelectionTimeout
			overrides["server_selection_timeout"] = serverSelectionTimeout
		}
		if dbOverrides.ConnectTimeout > 0 {
			connectTimeout = dbOverrides.ConnectTimeout
			overrides["connect_timeout"] = connectTimeout
		}
		if dbOverrides.SocketTimeout > 0 {
			socketTimeout = dbOverrides.SocketTimeout
			overrides["socket_timeout"] = socketTimeout
		}
		if dbOverrides.HeartbeatInterval > 0 {
			heartbeatInterval = dbOverrides.HeartbeatInterval
			overrides["heartbeat_interval"] = heartbeatInterval
		}
	}

	// Handle nil settings
	var retryWrites *bool
	if dbOverrides != nil && dbOverrides.RetryWrites != nil {
		retryWrites = dbOverrides.RetryWrites
		overrides["retry_writes"] = *retryWrites
	} else if defaults.RetryWrites != nil {
		retryWrites = defaults.RetryWrites
		overrides["retry_writes"] = *retryWrites
	}

	var topology *description.TopologyKind
	if dbOverrides != nil && dbOverrides.Topology != nil {
		topology = dbOverrides.Topology
		overrides["topology"] = *topology
	} else if defaults.Topology != nil {
		topology = defaults.Topology
		overrides["topology"] = *topology
	}

	// Set defaults ONLY if not configured by user
	if maxPoolSize == 0 {
		maxPoolSize = 10 // Only use default if user didn't provide a value
	}
	if minPoolSize == 0 {
		minPoolSize = 1 // Only use default if user didn't provide a value
	}
	if maxConnIdleTime == 0 {
		maxConnIdleTime = 30 * time.Second // Only use default if user didn't provide a value
	}
	if serverSelectionTimeout == 0 {
		serverSelectionTimeout = 30 * time.Second // Only use default if user didn't provide a value
	}
	if connectTimeout == 0 {
		connectTimeout = 30 * time.Second // Only use default if user didn't provide a value
	}
	if socketTimeout == 0 {
		socketTimeout = 30 * time.Second // Only use default if user didn't provide a value
	}
	if heartbeatInterval == 0 {
		heartbeatInterval = 10 * time.Second // Only use default if user didn't provide a value
	}

	// Log the configuration being used
	if len(overrides) > 0 {
		logger.Info("Using database-specific MongoDB client overrides",
			zap.String("database", dbName),
			zap.Any("overrides", overrides))
	} else {
		logger.Info("Using default MongoDB client settings",
			zap.String("database", dbName))
	}

	// Apply settings
	opts = opts.SetMaxPoolSize(uint64(maxPoolSize))
	opts = opts.SetMinPoolSize(uint64(minPoolSize))
	opts = opts.SetMaxConnIdleTime(maxConnIdleTime)
	opts = opts.SetServerSelectionTimeout(serverSelectionTimeout)
	opts = opts.SetConnectTimeout(connectTimeout)
	opts = opts.SetSocketTimeout(socketTimeout)
	opts = opts.SetHeartbeatInterval(heartbeatInterval)

	// Apply retry_writes setting if configured
	if retryWrites != nil {
		opts = opts.SetRetryWrites(*retryWrites)
	}

	// Log final configuration
	logFields := []zap.Field{
		zap.String("database", dbName),
		zap.Int("max_pool_size", maxPoolSize),
		zap.Int("min_pool_size", minPoolSize),
		zap.Duration("max_conn_idle_time", maxConnIdleTime),
		zap.Duration("server_selection_timeout", serverSelectionTimeout),
		zap.Duration("connect_timeout", connectTimeout),
		zap.Duration("socket_timeout", socketTimeout),
		zap.Duration("heartbeat_interval", heartbeatInterval),
	}

	if retryWrites != nil {
		logFields = append(logFields, zap.Bool("retry_writes", *retryWrites))
	}

	logger.Info("MongoDB client configuration applied", logFields...)

	return opts
}
