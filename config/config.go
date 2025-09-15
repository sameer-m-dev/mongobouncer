package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/sameer-m-dev/mongobouncer/util"
)

// TOMLConfig represents the complete configuration structure
type TOMLConfig struct {
	Mongobouncer MongobouncerConfig     `toml:"mongobouncer"`
	Databases    map[string]interface{} `toml:"databases"` // Can be string or Database struct
}

// Database represents database configuration
// Can be either a string (connection string) or a struct (detailed config)
type Database struct {
	ConnectionString string `toml:"connection_string"`
	Host             string `toml:"host"`
	Port             int    `toml:"port"`
	Database         string `toml:"database"`
	DBName           string `toml:"dbname"` // Alias for database
	User             string `toml:"user"`
	Password         string `toml:"password"`
	AuthDB           string `toml:"auth_db"`
	MaxConnections   int    `toml:"max_connections"`
	PoolMode         string `toml:"pool_mode"`
	PoolSize         int    `toml:"pool_size"`
	MaxDBConnections int    `toml:"max_db_connections"`
	Label            string `toml:"label"`

	// MongoDB client pool overrides (optional) - using shared config
	MongoDBConfig util.MongoDBClientConfig `toml:",inline"`
}
type MongobouncerConfig struct {
	// Core server settings
	ListenAddr string `toml:"listen_addr"`
	ListenPort int    `toml:"listen_port"`
	LogLevel   string `toml:"log_level"`
	LogFile    string `toml:"logfile"`

	// Connection pooling
	PoolMode      string `toml:"pool_mode"`
	MaxClientConn int    `toml:"max_client_conn"`

	// Metrics
	MetricsAddress string `toml:"metrics_address"`
	MetricsEnabled bool   `toml:"metrics_enabled"`

	// Authentication settings
	AuthEnabled bool `toml:"auth_enabled"`

	// Wildcard/Regex credential passthrough settings
	RegexCredentialPassthrough bool `toml:"regex_credential_passthrough"`

	// Network settings
	Network string `toml:"network"`
	Unlink  bool   `toml:"unlink"`

	// MongoDB client pool settings - using shared config
	MongoDBConfig util.MongoDBClientConfig `toml:",inline"`
	Ping          bool                     `toml:"ping"`
}

// Config represents the runtime configuration
type Config struct {
	tomlConfig *TOMLConfig
	logger     *zap.Logger
	metrics    util.MetricsInterface
	clients    []client
	network    string
	unlink     bool
	ping       bool
}

type client struct {
	address string
	label   string
	opts    *options.ClientOptions
}

// LoadConfig loads configuration from TOML file
func LoadConfig(configPath string, verbose bool) (*Config, error) {
	// Set default values
	config := &TOMLConfig{
		Mongobouncer: MongobouncerConfig{
			ListenAddr:                 "0.0.0.0",
			ListenPort:                 27017,
			LogLevel:                   "info",
			Network:                    "tcp4",
			PoolMode:                   "session",
			MaxClientConn:              100,
			MetricsAddress:             "localhost:9090",
			MetricsEnabled:             true,
			AuthEnabled:                true,
			RegexCredentialPassthrough: true,
		},
		Databases: make(map[string]interface{}),
	}

	// Load from file if provided
	if configPath != "" {
		if _, err := os.Stat(configPath); err != nil {
			return nil, fmt.Errorf("config file not found: %s", configPath)
		}

		var rawConfig map[string]interface{}
		if _, err := toml.DecodeFile(configPath, &rawConfig); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}

		// Parse the raw config into our structured config
		if err := parseRawConfig(rawConfig, config); err != nil {
			return nil, fmt.Errorf("failed to process config: %w", err)
		}
	}

	// Override log level if verbose
	if verbose {
		config.Mongobouncer.LogLevel = "debug"
	}

	// Create logger
	logger, err := createLogger(config.Mongobouncer.LogLevel, config.Mongobouncer.LogFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	// Create Metrics client
	var metricsClient util.MetricsInterface
	if config.Mongobouncer.MetricsEnabled {
		baseMetricsClient, err := util.NewMetricsClient(logger, config.Mongobouncer.MetricsAddress)
		if err != nil {
			logger.Warn("Failed to create metrics client", zap.Error(err))
			// Continue without metrics
		} else {
			// Wrap with batched metrics client for better performance
			batchedMetricsClient := util.NewBatchedMetricsClient(
				baseMetricsClient,
				logger,
				100,                  // batch size
				100*time.Millisecond, // flush interval
			)
			metricsClient = batchedMetricsClient
		}
	}

	// Build client configurations
	clients, err := buildClients(config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to build clients: %w", err)
	}

	return &Config{
		tomlConfig: config,
		logger:     logger,
		metrics:    metricsClient,
		clients:    clients,
		network:    config.Mongobouncer.Network,
		unlink:     config.Mongobouncer.Unlink,
		ping:       config.Mongobouncer.Ping,
	}, nil
}

// parseRawConfig parses the raw TOML configuration into our structured config
func parseRawConfig(rawConfig map[string]interface{}, config *TOMLConfig) error {
	// Parse mongobouncer section
	if mb, ok := rawConfig["mongobouncer"].(map[string]interface{}); ok {
		if listenAddr, ok := mb["listen_addr"].(string); ok {
			config.Mongobouncer.ListenAddr = listenAddr
		}
		if listenPort, ok := mb["listen_port"].(int64); ok {
			config.Mongobouncer.ListenPort = int(listenPort)
		}
		if logLevel, ok := mb["log_level"].(string); ok {
			config.Mongobouncer.LogLevel = logLevel
		}
		if logFile, ok := mb["logfile"].(string); ok {
			config.Mongobouncer.LogFile = logFile
		}
		if poolMode, ok := mb["pool_mode"].(string); ok {
			config.Mongobouncer.PoolMode = poolMode
		}
		if maxClientConn, ok := mb["max_client_conn"].(int64); ok {
			config.Mongobouncer.MaxClientConn = int(maxClientConn)
		}
		if metricsAddress, ok := mb["metrics_address"].(string); ok {
			config.Mongobouncer.MetricsAddress = metricsAddress
		}
		if metricsEnabled, ok := mb["metrics_enabled"].(bool); ok {
			config.Mongobouncer.MetricsEnabled = metricsEnabled
		}
		if network, ok := mb["network"].(string); ok {
			config.Mongobouncer.Network = network
		}
		if unlink, ok := mb["unlink"].(bool); ok {
			config.Mongobouncer.Unlink = unlink
		}
		if ping, ok := mb["ping"].(bool); ok {
			config.Mongobouncer.Ping = ping
		}
		if authEnabled, ok := mb["auth_enabled"].(bool); ok {
			config.Mongobouncer.AuthEnabled = authEnabled
		}
		if regexCredentialPassthrough, ok := mb["regex_credential_passthrough"].(bool); ok {
			config.Mongobouncer.RegexCredentialPassthrough = regexCredentialPassthrough
		}

		// Parse MongoDB client config fields
		if maxPoolSize, ok := mb["max_pool_size"].(int64); ok {
			config.Mongobouncer.MongoDBConfig.MaxPoolSize = int(maxPoolSize)
		}
		if minPoolSize, ok := mb["min_pool_size"].(int64); ok {
			config.Mongobouncer.MongoDBConfig.MinPoolSize = int(minPoolSize)
		}
		if maxConnIdleTime, ok := mb["max_conn_idle_time"].(string); ok {
			if duration, err := time.ParseDuration(maxConnIdleTime); err == nil {
				config.Mongobouncer.MongoDBConfig.MaxConnIdleTime = duration
			}
		}
		if serverSelectionTimeout, ok := mb["server_selection_timeout"].(string); ok {
			if duration, err := time.ParseDuration(serverSelectionTimeout); err == nil {
				config.Mongobouncer.MongoDBConfig.ServerSelectionTimeout = duration
			}
		}
		if connectTimeout, ok := mb["connect_timeout"].(string); ok {
			if duration, err := time.ParseDuration(connectTimeout); err == nil {
				config.Mongobouncer.MongoDBConfig.ConnectTimeout = duration
			}
		}
		if socketTimeout, ok := mb["socket_timeout"].(string); ok {
			if duration, err := time.ParseDuration(socketTimeout); err == nil {
				config.Mongobouncer.MongoDBConfig.SocketTimeout = duration
			}
		}
		if heartbeatInterval, ok := mb["heartbeat_interval"].(string); ok {
			if duration, err := time.ParseDuration(heartbeatInterval); err == nil {
				config.Mongobouncer.MongoDBConfig.HeartbeatInterval = duration
			}
		}
		if retryWrites, ok := mb["retry_writes"].(bool); ok {
			config.Mongobouncer.MongoDBConfig.RetryWrites = &retryWrites
		}
		if topology, ok := mb["topology"].(string); ok {
			topology, err := util.GetTopology(topology)
			if err != nil {
				return err
			}
			config.Mongobouncer.MongoDBConfig.Topology = &topology
		}
	}

	// Parse databases section
	if databases, ok := rawConfig["databases"].(map[string]interface{}); ok {
		config.Databases = databases
	}

	return nil
}

// validateDatabaseNames checks for conflicting database names/patterns
func validateDatabaseNames(databases map[string]interface{}, logger *zap.Logger) error {
	// Check if databases map is empty
	if len(databases) == 0 {
		return fmt.Errorf("no databases configured - MongoBouncer cannot run without database configuration")
	}

	// Track patterns and exact matches to detect conflicts
	exactMatches := make(map[string]string)     // dbName -> connectionString
	wildcardPatterns := make(map[string]string) // pattern -> connectionString
	prefixPatterns := make(map[string]string)   // pattern -> connectionString
	suffixPatterns := make(map[string]string)   // pattern -> connectionString
	containsPatterns := make(map[string]string) // pattern -> connectionString

	for dbName, dbConfigInterface := range databases {
		var connectionString string

		// Extract connection string for comparison
		switch dbConfig := dbConfigInterface.(type) {
		case string:
			connectionString = dbConfig
		case map[string]interface{}:
			if connStr, ok := dbConfig["connection_string"].(string); ok && connStr != "" {
				connectionString = connStr
			} else {
				// Build URI from individual fields for comparison
				host := "localhost"
				port := 27017

				if h, ok := dbConfig["host"].(string); ok {
					host = h
				}
				if p, ok := dbConfig["port"].(int64); ok {
					port = int(p)
				}

				var user, password string
				if u, ok := dbConfig["user"].(string); ok {
					user = u
				}
				if p, ok := dbConfig["password"].(string); ok {
					password = p
				}

				// Build URI
				if user != "" && password != "" {
					connectionString = fmt.Sprintf("mongodb://%s:%s@%s:%d", user, password, host, port)
				} else {
					connectionString = fmt.Sprintf("mongodb://%s:%d", host, port)
				}

				// Add database name if specified
				if dbname, ok := dbConfig["dbname"].(string); ok && dbname != "" {
					connectionString += "/" + dbname
				}
			}
		}

		// Check for wildcard route conflicts
		if dbName == "*" {
			if existing, exists := wildcardPatterns["*"]; exists {
				return fmt.Errorf("conflicting wildcard routes detected: both routes point to different connections (%s vs %s). Only one wildcard route (*) is allowed",
					sanitizeURI(existing), sanitizeURI(connectionString))
			}
			wildcardPatterns["*"] = connectionString
			continue
		}

		// Check for pattern-based conflicts
		if strings.Contains(dbName, "*") {
			// Note: We don't check for conflicts with exact matches because exact matches
			// always take precedence over pattern matches in the router

			// Check for conflicts with other patterns
			for pattern, patternConnStr := range prefixPatterns {
				if patternsConflict(dbName, pattern) {
					return fmt.Errorf("conflicting database patterns detected: '%s' and '%s' may match the same databases but point to different connections (%s vs %s)",
						dbName, pattern, sanitizeURI(connectionString), sanitizeURI(patternConnStr))
				}
			}
			for pattern, patternConnStr := range suffixPatterns {
				if patternsConflict(dbName, pattern) {
					return fmt.Errorf("conflicting database patterns detected: '%s' and '%s' may match the same databases but point to different connections (%s vs %s)",
						dbName, pattern, sanitizeURI(connectionString), sanitizeURI(patternConnStr))
				}
			}
			for pattern, patternConnStr := range containsPatterns {
				if patternsConflict(dbName, pattern) {
					return fmt.Errorf("conflicting database patterns detected: '%s' and '%s' may match the same databases but point to different connections (%s vs %s)",
						dbName, pattern, sanitizeURI(connectionString), sanitizeURI(patternConnStr))
				}
			}

			// Categorize the pattern
			if strings.HasPrefix(dbName, "*") && strings.HasSuffix(dbName, "*") {
				containsPatterns[dbName] = connectionString
			} else if strings.HasPrefix(dbName, "*") {
				suffixPatterns[dbName] = connectionString
			} else if strings.HasSuffix(dbName, "*") {
				prefixPatterns[dbName] = connectionString
			} else {
				containsPatterns[dbName] = connectionString
			}
		} else {
			// Exact match - no need to check for conflicts with patterns because exact matches
			// always take precedence over pattern matches in the router

			// Check for duplicate exact matches
			if existing, exists := exactMatches[dbName]; exists {
				return fmt.Errorf("duplicate database configuration detected: database '%s' is configured multiple times with different connections (%s vs %s)",
					dbName, sanitizeURI(existing), sanitizeURI(connectionString))
			}

			exactMatches[dbName] = connectionString
		}
	}

	logger.Info("Database configuration validation passed",
		zap.Int("exact_matches", len(exactMatches)),
		zap.Int("prefix_patterns", len(prefixPatterns)),
		zap.Int("suffix_patterns", len(suffixPatterns)),
		zap.Int("contains_patterns", len(containsPatterns)),
		zap.Bool("has_wildcard", len(wildcardPatterns) > 0))

	return nil
}

// matchesPattern checks if a database name matches a wildcard pattern
func matchesPattern(dbName, pattern string) bool {
	// Convert pattern to regex
	regexPattern := strings.ReplaceAll(pattern, "*", ".*")
	matched, err := regexp.MatchString("^"+regexPattern+"$", dbName)
	if err != nil {
		return false
	}
	return matched
}

// patternsConflict checks if two patterns could potentially match the same databases
func patternsConflict(pattern1, pattern2 string) bool {
	// Simple heuristic: if both patterns have wildcards in similar positions,
	// they might conflict. This is a conservative check.

	// Convert patterns to regex
	regex1 := strings.ReplaceAll(pattern1, "*", ".*")
	regex2 := strings.ReplaceAll(pattern2, "*", ".*")

	// Test with some common database name patterns to see if they overlap
	testNames := []string{
		"test", "prod", "staging", "dev",
		"test_db", "prod_db", "staging_db", "dev_db",
		"app_test", "app_prod", "app_staging", "app_dev",
		"test_app", "prod_app", "staging_app", "dev_app",
		"myapp_test", "myapp_prod", "myapp_staging", "myapp_dev",
	}

	matches1 := 0
	matches2 := 0

	for _, testName := range testNames {
		if matched, _ := regexp.MatchString("^"+regex1+"$", testName); matched {
			matches1++
		}
		if matched, _ := regexp.MatchString("^"+regex2+"$", testName); matched {
			matches2++
		}
	}

	// If both patterns match a significant number of test names, they might conflict
	return matches1 > 0 && matches2 > 0 && matches1 == matches2
}

// buildClients creates client configurations from the TOML config
func buildClients(config *TOMLConfig, logger *zap.Logger) ([]client, error) {
	var clients []client

	// Validate that databases are configured
	if len(config.Databases) == 0 {
		return nil, fmt.Errorf("no databases configured - MongoBouncer cannot run without database configuration")
	}

	// Validate database name conflicts
	if err := validateDatabaseNames(config.Databases, logger); err != nil {
		return nil, err
	}

	// For TOML config, we typically have one proxy listening address but multiple backend databases
	// Create a single client that will handle all database connections

	// Create listening address
	address := fmt.Sprintf("%s:%d", config.Mongobouncer.ListenAddr, config.Mongobouncer.ListenPort)

	// Process database configurations to determine the primary connection
	// For now, we'll use the first configured database as the main connection
	// In a full implementation, this would be handled by a router

	var primaryURI string
	var primaryLabel string

	for dbName, dbConfigInterface := range config.Databases {
		var uri string
		var label string

		switch dbConfig := dbConfigInterface.(type) {
		case string:
			// Simple connection string format
			uri = dbConfig
			label = dbName
		case map[string]interface{}:
			// Structured format - check for connection_string first
			if connStr, ok := dbConfig["connection_string"].(string); ok && connStr != "" {
				// Use connection_string if provided
				uri = connStr
			} else {
				// Build URI from individual fields
				host := "localhost"
				port := 27017

				if h, ok := dbConfig["host"].(string); ok {
					host = h
				}
				if p, ok := dbConfig["port"].(int64); ok {
					port = int(p)
				}

				var user, password string
				if u, ok := dbConfig["user"].(string); ok {
					user = u
				}
				if p, ok := dbConfig["password"].(string); ok {
					password = p
				}

				// Build URI
				if user != "" && password != "" {
					uri = fmt.Sprintf("mongodb://%s:%s@%s:%d", user, password, host, port)
				} else {
					uri = fmt.Sprintf("mongodb://%s:%d", host, port)
				}

				// Add database name if specified
				if dbname, ok := dbConfig["dbname"].(string); ok && dbname != "" {
					uri += "/" + dbname
				}
			}

			// Add connection options
			params := []string{}
			if maxPoolSize, ok := dbConfig["maxPoolSize"].(int64); ok && maxPoolSize > 0 {
				params = append(params, fmt.Sprintf("maxPoolSize=%d", maxPoolSize))
			} else if poolSize, ok := dbConfig["pool_size"].(int64); ok && poolSize > 0 {
				params = append(params, fmt.Sprintf("maxPoolSize=%d", poolSize))
			}
			if minPoolSize, ok := dbConfig["minPoolSize"].(int64); ok && minPoolSize > 0 {
				params = append(params, fmt.Sprintf("minPoolSize=%d", minPoolSize))
			}
			if maxIdleTimeMS, ok := dbConfig["maxIdleTimeMS"].(int64); ok && maxIdleTimeMS > 0 {
				params = append(params, fmt.Sprintf("maxIdleTimeMS=%d", maxIdleTimeMS))
			}
			if serverSelectionTimeoutMS, ok := dbConfig["serverSelectionTimeoutMS"].(int64); ok && serverSelectionTimeoutMS > 0 {
				params = append(params, fmt.Sprintf("serverSelectionTimeoutMS=%d", serverSelectionTimeoutMS))
			}

			if len(params) > 0 {
				uri += "?" + strings.Join(params, "&")
			}

			// Set label
			if l, ok := dbConfig["label"].(string); ok {
				label = l
			} else {
				label = dbName
			}
		}

		// Use the first database as primary (for simplicity)
		if primaryURI == "" {
			primaryURI = uri
			primaryLabel = label
		}

		logger.Info("Configured database",
			zap.String("name", dbName),
			zap.String("uri", sanitizeURI(uri)),
			zap.String("label", label))
	}

	// Create client options using the primary URI
	if primaryURI == "" {
		primaryURI = "mongodb://localhost:27017"
		primaryLabel = "default"
	}

	opts := options.Client().ApplyURI(primaryURI)

	// Create temporary config instance to apply MongoDB client settings
	tempConfig := &Config{tomlConfig: config, logger: logger}
	opts = tempConfig.ApplyMongoDBClientSettings(opts, "default", nil)

	clients = append(clients, client{
		address: address,
		label:   primaryLabel,
		opts:    opts,
	})

	return clients, nil
}

// createLogger creates a zap logger with the specified level and output
func createLogger(level, logFile string) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	switch strings.ToLower(level) {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn", "warning":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	config := zap.Config{
		Level:            zap.NewAtomicLevelAt(zapLevel),
		Development:      false,
		Encoding:         "json",
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	if logFile != "" {
		config.OutputPaths = []string{logFile}
	}

	return config.Build()
}

// ApplyMongoDBClientSettings applies MongoDB client pool settings from config
func (c *Config) ApplyMongoDBClientSettings(opts *options.ClientOptions, dbName string, dbConfig *Database) *options.ClientOptions {
	// Get defaults from mongobouncer config
	defaults := c.tomlConfig.Mongobouncer.MongoDBConfig

	// Convert dbConfig to util.MongoDBClientConfig if provided
	var dbOverrides *util.MongoDBClientConfig
	if dbConfig != nil {
		dbOverrides = &dbConfig.MongoDBConfig
	}

	// Use the shared utility function
	return util.ApplyMongoDBClientSettings(opts, dbName, defaults, dbOverrides, c.logger)
}

// sanitizeURI removes sensitive information from URI for logging
func sanitizeURI(uri string) string {
	// Replace password in URI with ***
	if strings.Contains(uri, "@") {
		parts := strings.Split(uri, "@")
		if len(parts) == 2 && strings.Contains(parts[0], "://") {
			userInfo := strings.Split(parts[0], "://")[1]
			if strings.Contains(userInfo, ":") {
				userParts := strings.Split(userInfo, ":")
				sanitized := userParts[0] + ":***"
				return strings.Replace(uri, userInfo, sanitized, 1)
			}
		}
	}
	return uri
}

// Interface methods for compatibility with existing code
func (c *Config) Logger() *zap.Logger {
	return c.logger
}

func (c *Config) Metrics() util.MetricsInterface {
	return c.metrics
}

func (c *Config) Network() string {
	return c.network
}

func (c *Config) Unlink() bool {
	return c.unlink
}

func (c *Config) Ping() bool {
	return c.ping
}

// GetDatabases returns the database configurations
func (c *Config) GetDatabases() map[string]Database {
	result := make(map[string]Database)

	for name, dbConfig := range c.tomlConfig.Databases {
		switch v := dbConfig.(type) {
		case string:
			// Simple connection string format is no longer supported
			// This will cause a validation error to help users understand the correct format
			panic(fmt.Sprintf("invalid database configuration for '%s': simple string format is no longer supported. Please use structured format with 'connection_string' field. Example: %s = { connection_string = \"%s\" }", name, name, v))
		case map[string]interface{}:
			// Structured format - convert to Database struct
			db := Database{}

			// connection_string is preferred, but individual fields are also supported
			if connStr, ok := v["connection_string"].(string); ok && connStr != "" {
				db.ConnectionString = connStr
			} else {
				// Fallback: build connection string from individual fields
				host := "localhost"
				port := 27017
				dbname := ""

				if h, ok := v["host"].(string); ok && h != "" {
					host = h
				}
				if p, ok := v["port"].(int64); ok && p > 0 {
					port = int(p)
				}
				if d, ok := v["dbname"].(string); ok && d != "" {
					dbname = d
				}

				// Build basic connection string from individual fields
				if dbname != "" {
					db.ConnectionString = fmt.Sprintf("mongodb://%s:%d/%s", host, port, dbname)
				} else {
					db.ConnectionString = fmt.Sprintf("mongodb://%s:%d", host, port)
				}
			}

			// Parse individual fields (optional overrides)
			if host, ok := v["host"].(string); ok {
				db.Host = host
			}
			if port, ok := v["port"].(int64); ok {
				db.Port = int(port)
			}
			if dbname, ok := v["dbname"].(string); ok {
				db.DBName = dbname
			}
			if user, ok := v["user"].(string); ok {
				db.User = user
			}
			if password, ok := v["password"].(string); ok {
				db.Password = password
			}
			if authdb, ok := v["auth_db"].(string); ok {
				db.AuthDB = authdb
			}
			if poolmode, ok := v["pool_mode"].(string); ok {
				db.PoolMode = poolmode
			}
			if poolsize, ok := v["pool_size"].(int64); ok {
				db.PoolSize = int(poolsize)
			}
			if maxdbconn, ok := v["max_db_connections"].(int64); ok {
				db.MaxDBConnections = int(maxdbconn)
			}
			if label, ok := v["label"].(string); ok {
				db.Label = label
			}

			// Parse MongoDB client pool overrides
			if maxpoolsize, ok := v["max_pool_size"].(int64); ok {
				db.MongoDBConfig.MaxPoolSize = int(maxpoolsize)
			}
			if minpoolsize, ok := v["min_pool_size"].(int64); ok {
				db.MongoDBConfig.MinPoolSize = int(minpoolsize)
			}
			if maxidletime, ok := v["max_conn_idle_time"].(string); ok {
				if duration, err := time.ParseDuration(maxidletime); err == nil {
					db.MongoDBConfig.MaxConnIdleTime = duration
				}
			}
			if seltimeout, ok := v["server_selection_timeout"].(string); ok {
				if duration, err := time.ParseDuration(seltimeout); err == nil {
					db.MongoDBConfig.ServerSelectionTimeout = duration
				}
			}
			if connecttimeout, ok := v["connect_timeout"].(string); ok {
				if duration, err := time.ParseDuration(connecttimeout); err == nil {
					db.MongoDBConfig.ConnectTimeout = duration
				}
			}
			if sockettimeout, ok := v["socket_timeout"].(string); ok {
				if duration, err := time.ParseDuration(sockettimeout); err == nil {
					db.MongoDBConfig.SocketTimeout = duration
				}
			}
			if heartbeatinterval, ok := v["heartbeat_interval"].(string); ok {
				if duration, err := time.ParseDuration(heartbeatinterval); err == nil {
					db.MongoDBConfig.HeartbeatInterval = duration
				}
			}
			if retrywrites, ok := v["retry_writes"].(bool); ok {
				db.MongoDBConfig.RetryWrites = &retrywrites
			}
			if topology, ok := v["topology"].(string); ok {
				topology, err := util.GetTopology(topology)
				if err != nil {
					c.logger.Fatal("Error verifying topology", zap.Error(err))
				}
				db.MongoDBConfig.Topology = &topology
			}
			result[name] = db
		}
	}

	return result
}

// GetMainConfig returns the main configuration
func (c *Config) GetMainConfig() MongobouncerConfig {
	return c.tomlConfig.Mongobouncer
}

// GetAuthEnabled returns whether authentication is enabled
func (c *Config) GetAuthEnabled() bool {
	return c.tomlConfig.Mongobouncer.AuthEnabled
}

// GetRegexCredentialPassthrough returns whether regex credential passthrough is enabled
func (c *Config) GetRegexCredentialPassthrough() bool {
	return c.tomlConfig.Mongobouncer.RegexCredentialPassthrough
}
