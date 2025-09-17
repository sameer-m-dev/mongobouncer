package config

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/sameer-m-dev/mongobouncer/mongo"
	"github.com/sameer-m-dev/mongobouncer/pool"
	"github.com/sameer-m-dev/mongobouncer/proxy"
)

// Proxies creates proxy instances from the configuration
func (c *Config) Proxies(log *zap.Logger) ([]*proxy.Proxy, error) {

	// Create distributed cache if enabled
	var distributedCache *mongo.DistributedCache
	sharedCacheConfig := c.GetSharedCacheConfig()
	if sharedCacheConfig.Enabled {
		// Parse duration strings
		sessionExpiry, err := time.ParseDuration(sharedCacheConfig.SessionExpiry)
		if err != nil {
			log.Warn("Invalid session_expiry duration, using default",
				zap.String("value", sharedCacheConfig.SessionExpiry),
				zap.Error(err))
			sessionExpiry = 30 * time.Minute
		}

		transactionExpiry, err := time.ParseDuration(sharedCacheConfig.TransactionExpiry)
		if err != nil {
			log.Warn("Invalid transaction_expiry duration, using default",
				zap.String("value", sharedCacheConfig.TransactionExpiry),
				zap.Error(err))
			transactionExpiry = 2 * time.Minute
		}

		cursorExpiry, err := time.ParseDuration(sharedCacheConfig.CursorExpiry)
		if err != nil {
			log.Warn("Invalid cursor_expiry duration, using default",
				zap.String("value", sharedCacheConfig.CursorExpiry),
				zap.Error(err))
			cursorExpiry = 24 * time.Hour
		}

		// Parse Kubernetes-style resource string for cache size
		cacheSizeQuantity, err := resource.ParseQuantity(sharedCacheConfig.CacheSizeBytes)
		if err != nil {
			log.Warn("Invalid cache_size_bytes format, using default",
				zap.String("value", sharedCacheConfig.CacheSizeBytes),
				zap.Error(err))
			cacheSizeQuantity = resource.MustParse("64Mi") // Default to 64Mi
		}
		cacheSizeBytes := cacheSizeQuantity.Value()

		distributedCacheConfig := &mongo.DistributedCacheConfig{
			Enabled:           sharedCacheConfig.Enabled,
			ListenAddr:        sharedCacheConfig.ListenAddr,
			ListenPort:        sharedCacheConfig.ListenPort,
			CacheSizeBytes:    cacheSizeBytes,
			SessionExpiry:     sessionExpiry,
			TransactionExpiry: transactionExpiry,
			CursorExpiry:      cursorExpiry,
			Debug:             sharedCacheConfig.Debug,
			LabelSelector:     sharedCacheConfig.LabelSelector,
			PeerURLs:          sharedCacheConfig.PeerURLs,
		}

		distributedCache = mongo.NewDistributedCache(distributedCacheConfig, log)

		// Start the distributed cache HTTP server
		if err := distributedCache.Start(); err != nil {
			log.Error("Failed to start distributed cache", zap.Error(err))
			return nil, err
		}

		log.Info("Distributed cache enabled",
			zap.String("listen_addr", sharedCacheConfig.ListenAddr),
			zap.Int("listen_port", sharedCacheConfig.ListenPort),
			zap.String("cache_size", sharedCacheConfig.CacheSizeBytes),
			zap.Int64("cache_size_bytes", cacheSizeBytes),
			zap.String("label_selector", sharedCacheConfig.LabelSelector),
			zap.Strings("peer_urls", sharedCacheConfig.PeerURLs))
	} else {
		log.Info("Distributed cache disabled")
	}

	// Create global session manager that will be shared across all database handlers
	var globalSessionManager *mongo.SessionManager
	if distributedCache != nil && distributedCache.IsEnabled() {
		globalSessionManager = mongo.NewSessionManagerWithDistributedCache(log, distributedCache)
	} else {
		globalSessionManager = mongo.NewSessionManager(log)
	}

	// Create database router for wildcard database support
	databaseRouter := proxy.NewDatabaseRouter(log)

	// Add database routes with precedence: exact match → wildcard match
	databases := c.GetDatabases()
	for dbName, dbConfig := range databases {
		// Create MongoDB client for this database
		var mongoClient *mongo.Mongo
		var err error
		if dbConfig.ConnectionString != "" {
			// Use connection string
			opts := options.Client().ApplyURI(dbConfig.ConnectionString)
			// Apply MongoDB client pool settings from config
			opts = c.ApplyMongoDBClientSettings(opts, dbName, &dbConfig)
			// Set ping to false to avoid startup failures - connections will be created lazily
			mongoClient, err = mongo.ConnectWithDistributedCache(log, c.metrics, opts, false, globalSessionManager, distributedCache)
			if err != nil {
				log.Warn("Failed to connect to database during startup, will retry on first request",
					zap.String("database", dbName),
					zap.Error(err))
				// Continue with nil client - will be created lazily on first request
				mongoClient = nil
			}
		} else {
			// Use structured config
			connectionString := fmt.Sprintf("mongodb://%s:%d/%s", dbConfig.Host, dbConfig.Port, dbConfig.DBName)
			opts := options.Client().ApplyURI(connectionString)
			// Apply MongoDB client pool settings from config
			opts = c.ApplyMongoDBClientSettings(opts, dbName, &dbConfig)
			// Set ping to false to avoid startup failures - connections will be created lazily
			mongoClient, err = mongo.ConnectWithDistributedCache(log, c.metrics, opts, false, globalSessionManager, distributedCache)
			if err != nil {
				log.Warn("Failed to connect to database during startup, will retry on first request",
					zap.String("database", dbName),
					zap.Error(err))
				// Continue with nil client - will be created lazily on first request
				mongoClient = nil
			}
		}

		// Add route to database router
		var connectionString string
		if dbConfig.ConnectionString != "" {
			connectionString = dbConfig.ConnectionString
		} else {
			connectionString = fmt.Sprintf("mongodb://%s:%d/%s", dbConfig.Host, dbConfig.Port, dbConfig.DBName)
		}

		routeConfig := &proxy.RouteConfig{
			DatabaseName:     dbName,
			Label:            dbName,
			MongoClient:      mongoClient,
			ConnectionString: connectionString,
			DatabaseConfig:   &dbConfig.MongoDBConfig,
		}
		databaseRouter.AddRoute(dbName, routeConfig)
	}

	// Create pool manager
	// Use MongoDB driver's pool settings for MongoBouncer's pool metrics
	mongodbMaxPoolSize := c.tomlConfig.Mongobouncer.MongoDBConfig.MaxPoolSize
	if mongodbMaxPoolSize == 0 {
		mongodbMaxPoolSize = 20 // Default if not configured
	}
	mongodbMinPoolSize := c.tomlConfig.Mongobouncer.MongoDBConfig.MinPoolSize
	if mongodbMinPoolSize == 0 {
		mongodbMinPoolSize = 3 // Default if not configured
	}

	poolManager := pool.NewManager(
		log,
		c.metrics,
		c.tomlConfig.Mongobouncer.PoolMode,
		mongodbMinPoolSize, // Use MongoDB driver's min pool size
		mongodbMaxPoolSize, // Use MongoDB driver's max pool size
		c.tomlConfig.Mongobouncer.MaxClientConn,
	)

	var proxies []*proxy.Proxy

	// Create a single proxy instance for the configured listen address
	listenAddress := fmt.Sprintf("%s:%d", c.tomlConfig.Mongobouncer.ListenAddr, c.tomlConfig.Mongobouncer.ListenPort)

	p, err := proxy.NewProxy(log, c.metrics, "mongobouncer", c.network, listenAddress, c.unlink, poolManager, databaseRouter, c.tomlConfig.Mongobouncer.AuthEnabled, c.tomlConfig.Mongobouncer.RegexCredentialPassthrough, c.tomlConfig.Mongobouncer.MongoDBConfig, globalSessionManager)
	if err != nil {
		return nil, err
	}

	// Register startup MongoDB clients for cleanup tracking
	p.RegisterStartupMongoClients(databaseRouter)

	proxies = append(proxies, p)

	return proxies, nil
}
