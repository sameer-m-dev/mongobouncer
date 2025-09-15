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

	mongos := make(map[string]*mongo.Mongo)
	for _, client := range c.clients {
		m, err := mongo.Connect(log, c.metrics, client.opts, c.ping)
		if err != nil {
			return nil, err
		}
		mongos[client.address] = m
	}

	mongoLookup := func(address string) *mongo.Mongo {
		return mongos[address]
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
			mongoClient, err = mongo.Connect(log, c.metrics, opts, c.ping)
			if err != nil {
				return nil, fmt.Errorf("failed to connect to database %s: %v", dbName, err)
			}
		} else {
			// Use structured config
			connectionString := fmt.Sprintf("mongodb://%s:%d/%s", dbConfig.Host, dbConfig.Port, dbConfig.DBName)
			opts := options.Client().ApplyURI(connectionString)
			// Apply MongoDB client pool settings from config
			opts = c.ApplyMongoDBClientSettings(opts, dbName, &dbConfig)
			mongoClient, err = mongo.Connect(log, c.metrics, opts, c.ping)
			if err != nil {
				return nil, fmt.Errorf("failed to connect to database %s: %v", dbName, err)
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
	for _, client := range c.clients {
		p, err := proxy.NewProxy(log, c.metrics, client.label, c.network, client.address, c.unlink, mongoLookup, poolManager, databaseRouter, c.tomlConfig.Mongobouncer.AuthEnabled, c.tomlConfig.Mongobouncer.RegexCredentialPassthrough, c.tomlConfig.Mongobouncer.MongoDBConfig)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, p)
	}

	return proxies, nil
}
