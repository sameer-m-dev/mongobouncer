package config

import (
	"fmt"

	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/mongo"
	"github.com/sameer-m-dev/mongobouncer/pool"
	"github.com/sameer-m-dev/mongobouncer/proxy"
)

// Proxies creates proxy instances from the configuration
func (c *Config) Proxies(log *zap.Logger) ([]*proxy.Proxy, error) {

	// Create global session manager that will be shared across all database handlers
	globalSessionManager := mongo.NewSessionManager(log)

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
			mongoClient, err = mongo.ConnectWithSessionManager(log, c.metrics, opts, false, globalSessionManager)
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
			mongoClient, err = mongo.ConnectWithSessionManager(log, c.metrics, opts, false, globalSessionManager)
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
