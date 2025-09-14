package proxy

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/sameer-m-dev/mongobouncer/pool"
	"github.com/sameer-m-dev/mongobouncer/util"
	"go.mongodb.org/mongo-driver/mongo/description"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/mongo"
)

type connection struct {
	log     *zap.Logger
	metrics *util.MetricsClient

	address string
	conn    net.Conn
	kill    chan interface{}
	buffer  []byte

	mongoLookup    MongoLookup
	poolManager    *pool.Manager
	databaseRouter *DatabaseRouter
	clientID       string

	// Context-aware routing fields
	intendedDatabase string       // The database the client originally wanted to connect to
	routeConfig      *RouteConfig // Cached route config for this connection

	// Client authentication fields
	clientUsername             string // Username from client connection string
	clientPassword             string // Password from client connection string
	authEnabled                bool   // Whether authentication is enabled
	regexCredentialPassthrough bool   // Whether to use client credentials for wildcard/regex matches

	// Database usage tracking
	databaseUsed string // The actual database that was used during this connection
}

func handleConnection(log *zap.Logger, metrics *util.MetricsClient, address string, conn net.Conn, mongoLookup MongoLookup, poolManager *pool.Manager, kill chan interface{}, databaseRouter *DatabaseRouter, authEnabled bool, regexCredentialPassthrough bool) string {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Connection crashed", zap.String("panic", fmt.Sprintf("%v", r)), zap.String("stack", string(debug.Stack())))
		}
	}()

	// Generate unique client ID
	clientID := fmt.Sprintf("%s-%d", address, time.Now().UnixNano())

	c := connection{
		log:     log,
		metrics: metrics,

		address: address,
		conn:    conn,
		kill:    kill,

		mongoLookup:                mongoLookup,
		poolManager:                poolManager,
		databaseRouter:             databaseRouter,
		clientID:                   clientID,
		authEnabled:                authEnabled,
		regexCredentialPassthrough: regexCredentialPassthrough,
	}

	// Extract target database from the client's connection string for smart admin routing
	// This helps us route admin operations to the correct MongoDB instance
	c.extractTargetDatabaseFromConnectionString()

	// Register client with pool manager for proper connection pooling
	// We'll register with a placeholder database and update it when we know the actual database
	if poolManager != nil {
		configuredPoolMode := poolManager.GetDefaultMode()
		_, err := poolManager.RegisterClient(clientID, "default", "default", configuredPoolMode)
		if err != nil {
			log.Error("Failed to register client with pool manager", zap.Error(err))
		} else {
			// Track active sessions
			if c.metrics != nil {
				_ = c.metrics.Gauge("sessions_active", 1, []string{}, 1)
			}
		}
	}

	// Ensure client is unregistered when connection closes
	defer func() {
		if poolManager != nil {
			poolManager.UnregisterClient(clientID)
			// Decrement active sessions
			if c.metrics != nil {
				_ = c.metrics.Gauge("sessions_active", -1, []string{}, 1)
			}
		}
	}()

	c.processMessages()

	// Return the database that was actually used during this connection
	return c.databaseUsed
}

func (c *connection) processMessages() {
	for {
		err := c.handleMessage()
		if err != nil {
			if err != io.EOF {
				select {
				case <-c.kill:
					// ignore errors from force shutdown
				default:
					c.log.Error("Error handling message", zap.Error(err))
				}
			}
			return
		}
	}
}

func (c *connection) handleMessage() (err error) {
	var tags []string

	defer func(start time.Time) {
		if c.metrics != nil {
			tags := append(tags, fmt.Sprintf("success:%v", err == nil))
			_ = c.metrics.Timing("handle_message", time.Since(start), tags, 1)
		}
	}(time.Now())

	var wm []byte
	if wm, err = c.readWireMessage(); err != nil {
		return
	}

	var op mongo.Operation
	if op, err = mongo.Decode(wm); err != nil {
		return
	}

	isMaster := op.IsIsMaster()
	command, collection := op.CommandAndCollection()
	unacknowledged := op.Unacknowledged()
	tags = append(
		tags,
		fmt.Sprintf("request_op_code:%v", op.OpCode()),
		fmt.Sprintf("is_master:%v", isMaster),
		fmt.Sprintf("command:%s", string(command)),
		fmt.Sprintf("collection:%s", collection),
		fmt.Sprintf("unacknowledged:%v", unacknowledged),
	)

	// Record request metrics
	if c.metrics != nil {
		// Extract database name from the operation
		databaseName := c.extractDatabaseName(op)

		// Record request count
		requestTags := []string{
			fmt.Sprintf("database:%s", databaseName),
			fmt.Sprintf("operation:%s", string(command)),
		}
		_ = c.metrics.Incr("request", requestTags, 1)

		// Record request size
		requestSize := float64(len(wm))
		requestSizeTags := []string{
			fmt.Sprintf("database:%s", databaseName),
		}
		_ = c.metrics.Distribution("request_size", requestSize, requestSizeTags, 1)

		// Record ismaster commands
		if isMaster {
			_ = c.metrics.Incr("ismaster_command", []string{}, 1)
		}
	}
	c.log.Debug(
		"Request",
		zap.Int32("op_code", int32(op.OpCode())),
		zap.Bool("is_master", isMaster),
		zap.String("command", string(command)),
		zap.String("collection", collection),
		zap.Int("request_size", len(wm)),
	)

	req := &mongo.Message{
		Wm: wm,
		Op: op,
	}
	var res *mongo.Message
	if res, err = c.roundTrip(req, isMaster, tags); err != nil {
		// Record error metrics
		if c.metrics != nil {
			databaseName := c.extractDatabaseName(op)
			errorTags := []string{
				fmt.Sprintf("database:%s", databaseName),
				fmt.Sprintf("error_type:%s", "round_trip_error"),
			}
			_ = c.metrics.Incr("error", errorTags, 1)
		}
		return
	}

	if unacknowledged {
		c.log.Debug("Unacknowledged request")
		return
	}

	tags = append(
		tags,
		fmt.Sprintf("response_op_code:%v", res.Op.OpCode()),
	)

	if _, err = c.conn.Write(res.Wm); err != nil {
		return
	}

	c.log.Debug(
		"Response",
		zap.Int32("op_code", int32(res.Op.OpCode())),
		zap.Int("response_size", len(res.Wm)),
	)

	// Record response metrics
	if c.metrics != nil {
		// Record response size
		responseSize := float64(len(res.Wm))
		databaseName := c.extractDatabaseName(op)
		responseSizeTags := []string{
			fmt.Sprintf("database:%s", databaseName),
		}
		_ = c.metrics.Distribution("response_size", responseSize, responseSizeTags, 1)
	}
	return
}

func (c *connection) readWireMessage() ([]byte, error) {
	var sizeBuf [4]byte

	_, err := io.ReadFull(c.conn, sizeBuf[:])
	if err != nil {
		return nil, err
	}

	// read the length as an int32
	size := (int32(sizeBuf[0])) | (int32(sizeBuf[1]) << 8) | (int32(sizeBuf[2]) << 16) | (int32(sizeBuf[3]) << 24)
	if int(size) > cap(c.buffer) {
		c.buffer = make([]byte, 0, size)
	}

	buffer := c.buffer[:size]
	copy(buffer, sizeBuf[:])

	_, err = io.ReadFull(c.conn, buffer[4:])
	if err != nil {
		return nil, err
	}

	return buffer, nil
}

func (c *connection) roundTrip(msg *mongo.Message, isMaster bool, tags []string) (*mongo.Message, error) {
	// Extract database name for pool routing (needed for authentication)
	databaseName := c.extractDatabaseName(msg.Op)

	// Handle isMaster commands with authentication and mocked response
	if isMaster {
		// Determine if this is an exact match or wildcard/regex match
		route, err := c.databaseRouter.GetRoute(databaseName)
		if err != nil {
			c.log.Error("Failed to get route", zap.Error(err))
			return nil, fmt.Errorf("routing failed: %v", err)
		}
		isExactMatch := route != nil && route.DatabaseName == databaseName

		// Authentication logic:
		// - For exact matches: ALWAYS require authentication
		// - For wildcard/regex matches: Only validate if credentials are provided in appName
		if c.authEnabled && c.clientUsername == "" {
			if isExactMatch {
				// For exact matches, ALWAYS require authentication
				c.log.Debug("Exact match detected, requiring authentication", zap.String("database", databaseName))
				if err := c.validateCredentialsFromAppName(msg, databaseName); err != nil {
					c.log.Error("AppName credential validation failed for exact match", zap.Error(err))
					return nil, fmt.Errorf("authentication failed: %v", err)
				}
			} else {
				// For wildcard/regex matches, only validate if appName credentials are provided
				appName, err := c.extractAppNameFromMessage(msg)
				if err != nil {
					c.log.Debug("Failed to extract appName", zap.Error(err))
					// If we can't extract appName, that's okay for wildcard matches
				} else if appName != "" {
					// Check if appName is in credential format (username:password)
					username, password, parseErr := c.parseAppNameCredentials(appName)
					if parseErr != nil {
						c.log.Debug("AppName is not in credential format, skipping authentication",
							zap.String("app_name", appName),
							zap.Error(parseErr))
						// This is okay for wildcard matches - we don't require credentials
					} else if username != "" && password != "" {
						// Only validate credentials if appName is in proper credential format
						c.log.Debug("Wildcard/regex match with appName credentials, validating", zap.String("database", databaseName))
						if err := c.validateCredentialsFromAppName(msg, databaseName); err != nil {
							c.log.Error("AppName credential validation failed for wildcard match", zap.Error(err))
							return nil, fmt.Errorf("authentication failed: %v", err)
						}
					} else {
						c.log.Debug("Wildcard/regex match with empty credentials, skipping authentication", zap.String("database", databaseName))
					}
				} else {
					c.log.Debug("Wildcard/regex match without appName credentials, skipping authentication", zap.String("database", databaseName))
				}
			}
		}

		requestID := msg.Op.RequestID()
		c.log.Debug("Mocking isMaster response", zap.Int32("request_id", requestID))
		// Always respond as a shard router (mongos) to emulate the behavior you described
		return mongo.IsMasterResponse(requestID, description.Sharded)
	}

	// For non-isMaster operations, authentication should already be validated
	// during the initial isMaster handshake

	// Get MongoDB client using simplified routing
	var mongoClient *mongo.Mongo
	if c.databaseRouter != nil {
		// Simple routing: use the database name directly
		targetDatabase := databaseName

		// Get route for the target database
		route, err := c.databaseRouter.GetRoute(targetDatabase)
		if err != nil {
			// If admin database is not configured but admin command is attempted, reject with proper error
			if targetDatabase == "admin" {
				c.log.Error("Admin command attempted but no admin database configured",
					zap.String("target_database", targetDatabase),
					zap.String("operation", msg.Op.String()),
					zap.Error(err))
				return nil, fmt.Errorf("admin database is not configured but admin command was attempted: %v", err)
			}
			return nil, fmt.Errorf("database %s is not supported: %v", targetDatabase, err)
		}

		// Cache the route config for this connection
		if c.routeConfig == nil {
			c.routeConfig = route
			c.intendedDatabase = targetDatabase
			c.log.Debug("Cached route config for connection",
				zap.String("intended_database", targetDatabase),
				zap.String("current_operation_db", databaseName))
		}

		// For wildcard/regex matches with credential passthrough, create a new MongoDB client
		// with the client's credentials instead of using the route's MongoDB client
		if c.regexCredentialPassthrough && c.isWildcardOrRegexMatch(targetDatabase, route) && c.clientUsername != "" {
			c.log.Debug("Using credential passthrough for MongoDB client creation",
				zap.String("username", c.clientUsername),
				zap.String("target_database", targetDatabase))

			// Create a new MongoDB client with the client's credentials
			clientWithCredentials, err := c.createMongoClientWithCredentials(route, c.clientUsername, c.clientPassword)
			if err != nil {
				c.log.Error("Failed to create MongoDB client with client credentials", zap.Error(err))
				return nil, fmt.Errorf("failed to create MongoDB client with client credentials: %v", err)
			}
			mongoClient = clientWithCredentials
		} else {
			// Use the route's MongoDB client for exact matches or when credential passthrough is disabled
			mongoClient = route.MongoClient
		}
	} else {
		return nil, fmt.Errorf("mongo client not found for address: %s", c.address)
	}

	// Use pool manager for connection pooling, fallback to direct client if needed
	if c.poolManager != nil {
		return c.roundTripWithPool(msg, databaseName, mongoClient, tags)
	}

	// Fallback to direct client usage
	return mongoClient.RoundTrip(msg, tags)
}

// roundTripWithPool handles round trip using the pool manager
func (c *connection) roundTripWithPool(msg *mongo.Message, databaseName string, mongoClient *mongo.Mongo, tags []string) (*mongo.Message, error) {
	// Use the extracted database name from the operation, not the client's database name
	actualDatabaseName := databaseName
	if actualDatabaseName == "" {
		actualDatabaseName = "default"
	}

	c.log.Debug("Using database name from operation",
		zap.String("extracted_db", databaseName),
		zap.String("actual_db", actualDatabaseName))

	// Ensure client is registered with the correct database
	// If this is the first operation for this database, re-register the client
	if c.poolManager != nil {
		// Check if client is registered for this database
		client, exists := c.poolManager.GetClient(c.clientID)
		if !exists || client.Database != actualDatabaseName {
			// Re-register client with the correct database
			c.log.Debug("Re-registering client with correct database",
				zap.String("client_id", c.clientID),
				zap.String("database", actualDatabaseName))

			// Unregister old client if exists
			if exists {
				c.poolManager.UnregisterClient(c.clientID)
			}

			// Register with correct database using the configured pool mode
			configuredPoolMode := c.poolManager.GetDefaultMode()
			_, err := c.poolManager.RegisterClient(c.clientID, "default", actualDatabaseName, configuredPoolMode)
			if err != nil {
				c.log.Error("Failed to re-register client with pool manager", zap.Error(err))
				// Return error instead of falling back to direct client usage
				return nil, err
			}
		}
	}

	// Get connection from pool (client is now registered with correct database)
	pooledConn, err := c.poolManager.GetConnection(c.clientID, actualDatabaseName, mongoClient, false, "")
	if err != nil {
		c.log.Warn("Failed to get connection from pool", zap.Error(err))
		// Return error instead of falling back to direct client usage
		// This ensures we respect connection pool limits
		return nil, err
	}

	// Use the pooled connection's MongoDB client for the round trip
	result, err := pooledConn.MongoClient.RoundTrip(msg, tags)

	// Determine if this is the end of a transaction based on operation type
	isEndOfTransaction := c.isEndOfTransaction(msg.Op)

	// Return connection to pool based on pool mode
	// For session mode, we don't return the connection immediately
	// For statement mode, we return after each operation
	// For transaction mode, we return based on transaction state
	c.poolManager.ReturnConnection(c.clientID, pooledConn, isEndOfTransaction)

	return result, err
}

// isEndOfTransaction determines if an operation marks the end of a transaction
func (c *connection) isEndOfTransaction(op mongo.Operation) bool {
	// For statement mode, every operation ends the transaction (connection should be returned)
	// For transaction mode, only specific operations end transactions
	// For session mode, connections are never returned until session ends

	// Get the command to determine transaction state
	command, _ := op.CommandAndCollection()
	commandStr := string(command)

	// Transaction-ending commands
	transactionEndCommands := []string{
		"commitTransaction",
		"abortTransaction",
		"endSession",
	}

	for _, endCmd := range transactionEndCommands {
		if commandStr == endCmd {
			return true
		}
	}

	// For statement mode, every operation should return the connection
	// This is handled in the pool manager based on pool mode
	return false
}

// extractTargetDatabaseFromConnectionString extracts the target database from the client's connection
// This is a heuristic approach: when a client connects to MongoBouncer, we assume they want to
// connect to the database specified in their connection string
func (c *connection) extractTargetDatabaseFromConnectionString() {
	c.log.Debug("Connection established, determining target database from connection context",
		zap.String("client_id", c.clientID),
		zap.String("address", c.address))

	// For now, we'll use a simple heuristic: if the client is connecting to our proxy,
	// we'll assume they want to connect to the first configured database
	// In a production implementation, we would parse the actual connection string
	// that the client used to connect to MongoBouncer

	// TODO: Implement proper connection string parsing to extract the target database
	// from the client's connection string (e.g., mongodb://localhost:27018/mongobouncer_test)
	// For now, we'll leave this empty and let the system determine the target database
	// from the first non-admin operation

	c.log.Debug("Connection established, will determine target database from first operation",
		zap.String("client_id", c.clientID))
}

// extractTargetDatabaseFromFirstMessage reads the first wire message and extracts the target database
// This allows us to determine the admin route immediately without buffering operations
func (c *connection) extractTargetDatabaseFromFirstMessage() {
	c.log.Debug("Connection established, reading first message to determine target database",
		zap.String("client_id", c.clientID))

	// Read the first wire message
	firstMsgBytes, err := c.readWireMessage()
	if err != nil {
		c.log.Error("Failed to read first wire message", zap.Error(err))
		return
	}

	// Decode the first message
	firstMsgOp, err := mongo.Decode(firstMsgBytes)
	if err != nil {
		c.log.Error("Failed to decode first wire message", zap.Error(err))
		return
	}

	// Try to extract the target database from the MongoDB handshake
	// For MongoDB clients, the target database is often specified in the connection string
	// We need to parse this from the initial handshake message
	targetDatabase := c.extractTargetDatabaseFromHandshake(firstMsgOp)

	if targetDatabase != "" {
		c.intendedDatabase = targetDatabase
		c.log.Debug("Determined target database from handshake",
			zap.String("intended_database", c.intendedDatabase),
			zap.String("client_id", c.clientID))
	} else {
		c.log.Debug("Could not determine target database from handshake, will wait for operation",
			zap.String("client_id", c.clientID))
	}

	// Process the first message immediately
	err = c.handleMessageWithBytes(firstMsgBytes, firstMsgOp)
	if err != nil {
		c.log.Error("Error handling first message", zap.Error(err))
		return
	}

	// Continue processing subsequent messages
	c.processMessages()
}

// extractTargetDatabaseFromHandshake extracts the target database from MongoDB handshake messages
func (c *connection) extractTargetDatabaseFromHandshake(op mongo.Operation) string {
	// For MongoDB handshake operations (like isMaster, hello), we need to look for
	// the target database in the BSON payload of the message

	// Check if this is a handshake operation
	if !op.IsIsMaster() {
		return ""
	}

	// Log detailed information about the handshake operation to understand what we're receiving
	command, collection := op.CommandAndCollection()
	c.log.Debug("Handshake operation detected - analyzing for target database",
		zap.String("operation_type", fmt.Sprintf("%T", op)),
		zap.String("operation_string", op.String()),
		zap.String("database_name", op.DatabaseName()),
		zap.String("command", string(command)),
		zap.String("collection", collection),
		zap.String("request_id", fmt.Sprintf("%d", op.RequestID())))

	// Try to extract target database from the operation's BSON payload
	// For MongoDB handshake operations, the target database information might be
	// embedded in the BSON document that was sent
	targetDatabase := c.extractTargetDatabaseFromBSON(op)
	if targetDatabase != "" {
		c.log.Debug("Found target database in handshake BSON",
			zap.String("target_database", targetDatabase))
		return targetDatabase
	}

	// If we can't determine the target database from the handshake, return empty
	c.log.Debug("Could not extract target database from handshake operation")
	return ""
}

// extractTargetDatabaseFromBSON attempts to extract target database from BSON payload
func (c *connection) extractTargetDatabaseFromBSON(op mongo.Operation) string {
	// For MongoDB operations, we need to access the raw BSON data
	// The target database information might be in the connection metadata

	// Try to get the database name from the operation itself first
	databaseName := op.DatabaseName()
	if databaseName != "" && databaseName != "admin" {
		c.log.Debug("Found target database in operation database name",
			zap.String("database", databaseName))
		return databaseName
	}

	// For handshake operations, we'll analyze the operation string
	// to look for patterns that might indicate the target database
	operationString := op.String()
	c.log.Debug("Analyzing operation string for target database",
		zap.String("operation_string", operationString))

	// Look for patterns in the operation string that might indicate the target database
	// This is a heuristic approach - we'll look for database names in the string
	// that are not "admin"

	// For now, we'll return empty and let the normal flow handle it
	// The detailed logging will help us understand what information is available
	return ""
}

// handleMessageWithBytes handles a message with pre-decoded bytes and operation
func (c *connection) handleMessageWithBytes(wm []byte, op mongo.Operation) (err error) {
	var tags []string

	defer func(start time.Time) {
		if c.metrics != nil {
			tags := append(tags, fmt.Sprintf("success:%v", err == nil))
			_ = c.metrics.Timing("handle_message", time.Since(start), tags, 1)
		}
	}(time.Now())

	isMaster := op.IsIsMaster()
	command, collection := op.CommandAndCollection()
	unacknowledged := op.Unacknowledged()
	tags = append(
		tags,
		fmt.Sprintf("request_op_code:%v", op.OpCode()),
		fmt.Sprintf("is_master:%v", isMaster),
		fmt.Sprintf("command:%s", string(command)),
		fmt.Sprintf("collection:%s", collection),
		fmt.Sprintf("unacknowledged:%v", unacknowledged),
	)

	// Record request metrics
	if c.metrics != nil {
		// Extract database name from the operation
		databaseName := c.extractDatabaseName(op)

		// Record request count
		requestTags := append(tags, fmt.Sprintf("database:%s", databaseName))
		_ = c.metrics.Incr("request", requestTags, 1)

		// Record request size
		requestSize := len(wm)
		_ = c.metrics.Distribution("request_size", float64(requestSize), requestTags, 1)
	}

	c.log.Debug("Request",
		zap.String("op_code", fmt.Sprintf("%v", op.OpCode())),
		zap.Bool("is_master", isMaster),
		zap.String("command", string(command)),
		zap.String("collection", collection),
		zap.Int("request_size", len(wm)))

	// Perform round trip
	res, err := c.roundTrip(&mongo.Message{Wm: wm, Op: op}, false, tags)
	if err != nil {
		return err
	}

	// Record response metrics
	if c.metrics != nil {
		responseSize := len(res.Wm)
		responseSizeTags := append(tags, fmt.Sprintf("response_op_code:%v", res.Op.OpCode()))
		_ = c.metrics.Distribution("response_size", float64(responseSize), responseSizeTags, 1)
	}

	if _, err = c.conn.Write(res.Wm); err != nil {
		return
	}
	return
}

// extractDatabaseName extracts the database name from a MongoDB operation
func (c *connection) extractDatabaseName(op mongo.Operation) string {
	// Try to extract from collection name first (most reliable)
	_, collection := op.CommandAndCollection()
	c.log.Debug("Extracting database name",
		zap.String("collection", collection),
		zap.String("op_type", fmt.Sprintf("%T", op)),
		zap.String("database_name", op.DatabaseName()),
		zap.String("operation_string", op.String()))

	var databaseName string

	if collection != "" {
		// Collection names are in format "database.collection"
		parts := strings.SplitN(collection, ".", 2)
		if len(parts) >= 2 {
			databaseName = parts[0]
			c.log.Debug("Extracted database name from collection",
				zap.String("collection", collection),
				zap.String("database", databaseName))
		}
	}

	// Use the new DatabaseName method as fallback
	if databaseName == "" {
		if dbName := op.DatabaseName(); dbName != "" {
			databaseName = dbName
			c.log.Debug("Extracted database name from operation", zap.String("database", databaseName))
		}
	}

	// Default fallback
	if databaseName == "" {
		databaseName = "default"
		c.log.Debug("Using default database name", zap.String("collection", collection))
	}

	// Track the database name for this connection (only for non-admin operations)
	if databaseName != "admin" && databaseName != "" {
		c.databaseUsed = databaseName
		c.log.Debug("Updated connection database tracking",
			zap.String("database_used", c.databaseUsed),
			zap.String("current_operation_db", databaseName))
	}

	return databaseName
}

// determineTargetDatabase determines which database this operation should be routed to
func (c *connection) determineTargetDatabase(currentDatabase string, op mongo.Operation) string {
	// If we already have a cached route config, check if it matches the current operation
	if c.routeConfig != nil {
		// If the current operation is for a different database than what we have cached,
		// we need to handle this properly
		if currentDatabase != c.intendedDatabase {
			// This is an operation for a different database
			// We should route to the actual database, not use the cached route
			c.log.Debug("Operation for different database than cached",
				zap.String("current_db", currentDatabase),
				zap.String("cached_intended_db", c.intendedDatabase))
			return currentDatabase
		}
		// For operations matching our cached database, use cached route
		return c.intendedDatabase
	}

	// Use the current database as the intended database
	c.intendedDatabase = currentDatabase

	c.log.Debug("Set intended database from operation",
		zap.String("intended_database", currentDatabase),
		zap.String("current_operation_db", currentDatabase))
	return currentDatabase
}

// extractHostFromRoute extracts host and port from a route configuration
func (c *connection) extractHostFromRoute(targetRoute *RouteConfig) (string, string) {
	// Extract host from the connection string
	if targetRoute.ConnectionString == "" {
		return "", ""
	}

	c.log.Debug("Parsing connection string for host/port extraction",
		zap.String("connection_string", targetRoute.ConnectionString))

	// Parse the connection string to extract host and port
	// Format: mongodb://[username:password@]host:port/database

	// Simple parsing - look for mongodb:// and extract host:port
	if !strings.HasPrefix(targetRoute.ConnectionString, "mongodb://") {
		return "", ""
	}

	// Remove mongodb:// prefix
	withoutScheme := strings.TrimPrefix(targetRoute.ConnectionString, "mongodb://")

	// Handle credentials - look for @ symbol
	var hostPortPart string
	if atIndex := strings.Index(withoutScheme, "@"); atIndex != -1 {
		// Has credentials, extract everything after @
		hostPortPart = withoutScheme[atIndex+1:]
	} else {
		// No credentials
		hostPortPart = withoutScheme
	}

	// Find the first '/' to separate host:port from database
	slashIndex := strings.Index(hostPortPart, "/")
	if slashIndex == -1 {
		// No database specified, use the whole hostPortPart
		hostPort := hostPortPart
		c.log.Debug("Extracted host:port from connection string",
			zap.String("host_port", hostPort))

		// Extract host and port
		host, port := c.extractHostAndPort(hostPort)

		c.log.Debug("Parsed host and port",
			zap.String("host", host),
			zap.String("port", port))

		return host, port
	}

	// Extract host:port part
	hostPort := hostPortPart[:slashIndex]

	c.log.Debug("Extracted host:port from connection string",
		zap.String("host_port", hostPort))

	// Extract host and port
	host, port := c.extractHostAndPort(hostPort)

	c.log.Debug("Parsed host and port",
		zap.String("host", host),
		zap.String("port", port))

	return host, port
}

// extractHostAndPort extracts host and port from host:port string
func (c *connection) extractHostAndPort(hostPort string) (string, string) {
	// Handle IPv6 addresses in brackets
	if strings.HasPrefix(hostPort, "[") {
		// IPv6 format: [::1]:27017
		closeBracket := strings.Index(hostPort, "]")
		if closeBracket == -1 {
			return "", ""
		}
		host := hostPort[1:closeBracket]
		if closeBracket+1 < len(hostPort) && hostPort[closeBracket+1] == ':' {
			port := hostPort[closeBracket+2:]
			return host, port
		}
		return host, "27017" // Default MongoDB port
	}

	// IPv4 format: localhost:27017
	colonIndex := strings.LastIndex(hostPort, ":")
	if colonIndex == -1 {
		return hostPort, "27017" // Default MongoDB port
	}

	host := hostPort[:colonIndex]
	port := hostPort[colonIndex+1:]
	return host, port
}

// extractCredentialsFromConnectionString extracts username and password from MongoDB connection string
func (c *connection) extractCredentialsFromConnectionString(connStr string) (username, password string, err error) {
	// Parse the connection string
	parsedURL, err := url.Parse(connStr)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse connection string: %v", err)
	}

	// Extract username and password from URL
	if parsedURL.User != nil {
		username = parsedURL.User.Username()
		password, _ = parsedURL.User.Password()
	}

	c.log.Debug("Extracted credentials from connection string",
		zap.String("username", username),
		zap.Bool("has_password", password != ""))

	return username, password, nil
}

// validateCredentialsFromAppName extracts and validates credentials from appName parameter in isMaster calls
func (c *connection) validateCredentialsFromAppName(msg *mongo.Message, targetDatabase string) error {
	c.log.Debug("Validating credentials from appName", zap.String("target_database", targetDatabase), zap.Bool("auth_enabled", c.authEnabled))

	// If authentication is disabled, skip validation
	if !c.authEnabled {
		c.log.Debug("Authentication is disabled, skipping credential validation")
		return nil
	}

	// Extract appName from the message
	appName, err := c.extractAppNameFromMessage(msg)
	if err != nil {
		return fmt.Errorf("failed to extract appName: %v", err)
	}

	if appName == "" {
		c.log.Debug("No appName found in message")
		// For exact matches, this is an error. For wildcard matches, this is okay.
		// The calling code should handle this distinction.
		return fmt.Errorf("authentication required: appName parameter must be provided")
	}

	c.log.Debug("Extracted appName", zap.String("app_name", appName))

	// Parse appName to extract username and password (format: username:password)
	username, password, err := c.parseAppNameCredentials(appName)
	if err != nil {
		c.log.Debug("AppName is not in credential format", zap.Error(err))
		return fmt.Errorf("invalid appName format: must be 'username:password', got: %s", appName)
	}

	c.log.Debug("Parsed credentials from appName",
		zap.String("username", username),
		zap.Bool("has_password", password != ""))

	// Validate credentials against database configuration
	if err := c.validateCredentialsAgainstDatabase(username, password, targetDatabase); err != nil {
		c.log.Error("Credential validation failed", zap.Error(err))
		return fmt.Errorf("credential validation failed: %v", err)
	}

	// Store validated credentials for this connection
	c.clientUsername = username
	c.clientPassword = password

	c.log.Debug("Credentials validated successfully from appName",
		zap.String("username", username),
		zap.String("target_database", targetDatabase))

	return nil
}

// extractAppNameFromMessage extracts appName from isMaster call
func (c *connection) extractAppNameFromMessage(msg *mongo.Message) (string, error) {
	opStr := msg.Op.String()
	c.log.Debug("Analyzing operation string for appName", zap.String("operation_string", opStr))

	// Look for application.name in the operation string
	if strings.Contains(opStr, "application") && strings.Contains(opStr, "name") {
		// Extract the appName value - look for "name": "value"
		start := strings.Index(opStr, "\"name\": \"")
		if start != -1 {
			start += 9 // Skip "\"name\": \""
			end := strings.Index(opStr[start:], "\"")
			if end != -1 {
				appName := opStr[start : start+end]
				c.log.Debug("Extracted appName from isMaster call", zap.String("app_name", appName))
				return appName, nil
			}
		}
	}

	c.log.Debug("No appName found in isMaster message")
	return "", nil
}

// parseAppNameCredentials parses appName string to extract username and password
func (c *connection) parseAppNameCredentials(appName string) (username, password string, err error) {
	// Expected format: username:password
	parts := strings.Split(appName, ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("appName must be in format 'username:password', got: %s", appName)
	}

	username = strings.TrimSpace(parts[0])
	password = strings.TrimSpace(parts[1])

	if username == "" || password == "" {
		return "", "", fmt.Errorf("username and password cannot be empty")
	}

	return username, password, nil
}

// validateCredentialsAgainstDatabase validates credentials against database configuration
func (c *connection) validateCredentialsAgainstDatabase(username, password, targetDatabase string) error {
	// Get the route configuration for the target database
	route, err := c.databaseRouter.GetRoute(targetDatabase)
	if err != nil {
		return fmt.Errorf("database %s is not supported: %v", targetDatabase, err)
	}

	// Check if this is a wildcard/regex match and credential passthrough is enabled
	if c.regexCredentialPassthrough && c.isWildcardOrRegexMatch(targetDatabase, route) {
		c.log.Debug("Using credential passthrough for wildcard/regex match",
			zap.String("username", username),
			zap.String("target_database", targetDatabase))

		// Store the client credentials for use in downstream connections
		c.clientUsername = username
		c.clientPassword = password

		// For wildcard/regex matches, we don't validate against route credentials
		// Instead, we'll use the client credentials for the downstream connection
		return nil
	}

	// For exact matches, validate credentials against database configuration
	routeUsername, routePassword, err := c.extractCredentialsFromConnectionString(route.ConnectionString)
	if err != nil {
		return fmt.Errorf("failed to extract route credentials: %v", err)
	}

	// Validate credentials
	if username != routeUsername || password != routePassword {
		c.log.Error("Client credentials do not match route credentials",
			zap.String("client_username", username),
			zap.String("route_username", routeUsername),
			zap.String("target_database", targetDatabase))
		return fmt.Errorf("authentication failed: invalid credentials")
	}

	c.log.Debug("Credentials match database configuration",
		zap.String("username", username),
		zap.String("target_database", targetDatabase))

	// Store validated credentials for this connection
	c.clientUsername = username
	c.clientPassword = password

	return nil
}

// isWildcardOrRegexMatch checks if the target database matches a wildcard or regex pattern
func (c *connection) isWildcardOrRegexMatch(targetDatabase string, route *RouteConfig) bool {
	// Check if the route's database name contains wildcards or regex patterns
	routePattern := route.DatabaseName

	// Check for wildcard patterns
	if strings.Contains(routePattern, "*") {
		c.log.Debug("Route pattern contains wildcard",
			zap.String("route_pattern", routePattern),
			zap.String("target_database", targetDatabase))
		return true
	}

	// Check if this is a wildcard route (matches everything)
	if routePattern == "*" {
		c.log.Debug("Route is wildcard route",
			zap.String("target_database", targetDatabase))
		return true
	}

	// Check if this route was added as a pattern route by checking if the database name
	// doesn't exactly match the route pattern (indicating it was matched via pattern)
	if routePattern != targetDatabase {
		c.log.Debug("Route pattern doesn't match target database exactly - likely a pattern match",
			zap.String("route_pattern", routePattern),
			zap.String("target_database", targetDatabase))
		return true
	}

	c.log.Debug("Route is exact match",
		zap.String("route_pattern", routePattern),
		zap.String("target_database", targetDatabase))

	return false
}

// createMongoClientWithCredentials creates a MongoDB client using the client's credentials
func (c *connection) createMongoClientWithCredentials(route *RouteConfig, username, password string) (*mongo.Mongo, error) {
	// Extract host and port from the route's connection string
	host, port := c.extractHostFromRoute(route)
	if host == "" || port == "" {
		return nil, fmt.Errorf("failed to extract host/port from route connection string")
	}

	// Parse the original route connection string to extract authSource and other options
	originalURI := route.ConnectionString
	parsedURI, err := url.Parse(originalURI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse route connection string: %v", err)
	}

	// Build new connection string with client credentials
	var newConnectionString string
	if username != "" && password != "" {
		// Use client credentials but preserve authSource and other options from route
		newConnectionString = fmt.Sprintf("mongodb://%s:%s@%s:%s/", username, password, host, port)

		// Add authSource if it was specified in the original route
		if parsedURI.Query().Get("authSource") != "" {
			newConnectionString += "?authSource=" + parsedURI.Query().Get("authSource")
		} else {
			// Default to admin authSource for client credentials
			newConnectionString += "?authSource=admin"
		}

		// Add other query parameters from the original route
		query := parsedURI.Query()
		for key, values := range query {
			if key != "authSource" { // We already handled authSource above
				for _, value := range values {
					if strings.Contains(newConnectionString, "?") {
						newConnectionString += "&" + key + "=" + value
					} else {
						newConnectionString += "?" + key + "=" + value
					}
				}
			}
		}
	} else {
		newConnectionString = fmt.Sprintf("mongodb://%s:%s/", host, port)
		// Preserve query parameters from original route
		if parsedURI.RawQuery != "" {
			newConnectionString += "?" + parsedURI.RawQuery
		}
	}

	c.log.Debug("Creating MongoDB client with client credentials",
		zap.String("username", username),
		zap.String("host", host),
		zap.String("port", port),
		zap.String("auth_source", parsedURI.Query().Get("authSource")),
		zap.String("connection_string", sanitizeConnectionString(newConnectionString)))

	// Create MongoDB client options
	opts := options.Client().ApplyURI(newConnectionString)

	// Create the MongoDB client
	mongoClient, err := mongo.Connect(c.log, c.metrics, opts, false) // Don't ping for dynamic clients
	if err != nil {
		return nil, fmt.Errorf("failed to create MongoDB client with client credentials: %v", err)
	}

	return mongoClient, nil
}

// sanitizeConnectionString removes sensitive information from connection string for logging
func sanitizeConnectionString(connStr string) string {
	// Replace password in URI with ***
	if strings.Contains(connStr, "@") {
		parts := strings.Split(connStr, "@")
		if len(parts) == 2 && strings.Contains(parts[0], "://") {
			userInfo := strings.Split(parts[0], "://")[1]
			if strings.Contains(userInfo, ":") {
				userParts := strings.Split(userInfo, ":")
				sanitized := userParts[0] + ":***"
				return strings.Replace(connStr, userInfo, sanitized, 1)
			}
		}
	}
	return connStr
}
