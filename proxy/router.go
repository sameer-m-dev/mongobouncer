package proxy

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/lruttl"
	"github.com/sameer-m-dev/mongobouncer/mongo"
	"github.com/sameer-m-dev/mongobouncer/util"
)

// DatabaseRouter handles routing of database connections with wildcard support
type DatabaseRouter struct {
	logger        *zap.Logger
	routes        map[string]*RouteConfig // Exact database name matches
	wildcardRoute *RouteConfig            // Wildcard (*) fallback route
	patternRoutes []*PatternRoute         // Pattern-based routes (e.g., test_*, *_prod)
	mutex         sync.RWMutex
	routeCache    *lruttl.Cache // Cache for database route resolutions
}

// RouteConfig represents configuration for routing to a specific MongoDB instance
type RouteConfig struct {
	DatabaseName      string
	MongoClient       *mongo.Mongo
	ConnectionString  string
	PoolMode          string
	MaxConnections    int
	Label             string
	DatabaseConfig    *util.MongoDBClientConfig
	clientMutex       sync.Mutex    // Mutex for thread-safe lazy client creation
	CredentialClients *lruttl.Cache // LRU cache with TTL for credential-specific clients
}

// PatternRoute represents a pattern-based route
type PatternRoute struct {
	Pattern     string
	PatternType PatternType
	RouteConfig *RouteConfig
}

// PatternType represents the type of pattern matching
type PatternType int

const (
	ExactMatch PatternType = iota
	PrefixMatch
	SuffixMatch
	ContainsMatch
	WildcardMatch
)

// NewDatabaseRouter creates a new database router
func NewDatabaseRouter(logger *zap.Logger) *DatabaseRouter {
	// Create route cache with 5-minute TTL and max 1000 entries
	routeCache := lruttl.New(1000, 5*time.Minute)

	return &DatabaseRouter{
		logger:        logger,
		routes:        make(map[string]*RouteConfig),
		patternRoutes: make([]*PatternRoute, 0),
		routeCache:    routeCache,
	}
}

// AddRoute adds a database route
func (r *DatabaseRouter) AddRoute(pattern string, config *RouteConfig) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Clear cache when routes are modified
	r.routeCache.Clear()

	// Normalize pattern to lowercase for case-insensitive handling
	normalizedPattern := util.NormalizeDatabaseName(pattern)

	// Handle wildcard route
	if normalizedPattern == "*" {
		r.wildcardRoute = config
		r.logger.Info("Added wildcard route", zap.String("label", config.Label))
		return nil
	}

	// Handle pattern routes
	if strings.Contains(normalizedPattern, "*") {
		patternRoute := &PatternRoute{
			Pattern:     normalizedPattern,
			RouteConfig: config,
		}

		// Determine pattern type
		if strings.HasPrefix(normalizedPattern, "*") && strings.HasSuffix(normalizedPattern, "*") {
			patternRoute.PatternType = ContainsMatch
			patternRoute.Pattern = strings.Trim(normalizedPattern, "*")
		} else if strings.HasPrefix(normalizedPattern, "*") {
			patternRoute.PatternType = SuffixMatch
			patternRoute.Pattern = strings.TrimPrefix(normalizedPattern, "*")
		} else if strings.HasSuffix(normalizedPattern, "*") {
			patternRoute.PatternType = PrefixMatch
			patternRoute.Pattern = strings.TrimSuffix(normalizedPattern, "*")
		} else {
			// Handle wildcard patterns with * in the middle
			patternRoute.PatternType = WildcardMatch
			patternRoute.Pattern = normalizedPattern
		}

		r.patternRoutes = append(r.patternRoutes, patternRoute)
		r.logger.Info("Added pattern route",
			zap.String("pattern", pattern),
			zap.String("normalized_pattern", normalizedPattern),
			zap.String("label", config.Label))
		return nil
	}

	// Handle exact match
	r.routes[normalizedPattern] = config
	r.logger.Info("Added exact route",
		zap.String("database", pattern),
		zap.String("normalized_database", normalizedPattern),
		zap.String("label", config.Label))
	return nil
}

// RemoveRoute removes a database route
func (r *DatabaseRouter) RemoveRoute(pattern string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Clear cache when routes are modified
	r.routeCache.Clear()

	if pattern == "*" {
		r.wildcardRoute = nil
		return
	}

	delete(r.routes, pattern)

	// Remove from pattern routes
	filtered := make([]*PatternRoute, 0)
	for _, pr := range r.patternRoutes {
		if pr.Pattern != pattern {
			filtered = append(filtered, pr)
		}
	}
	r.patternRoutes = filtered
}

// GetRoute returns the route configuration for a given database name
func (r *DatabaseRouter) GetRoute(databaseName string) (*RouteConfig, error) {
	// Normalize database name to lowercase for case-insensitive handling
	normalizedDbName := util.NormalizeDatabaseName(databaseName)

	// Check cache first
	if cachedRoute, found := r.routeCache.Get(normalizedDbName); found {
		if route, ok := cachedRoute.(*RouteConfig); ok {
			r.logger.Debug("Found cached route",
				zap.String("database", databaseName),
				zap.String("label", route.Label))
			return route, nil
		}
	}

	r.mutex.RLock()
	defer r.mutex.RUnlock()

	var route *RouteConfig
	var err error

	// 1. Try exact match first
	if exactRoute, ok := r.routes[normalizedDbName]; ok {
		route = exactRoute
		r.logger.Debug("Found exact match route",
			zap.String("database", databaseName),
			zap.String("normalized_database", normalizedDbName),
			zap.String("label", route.Label))
	} else {
		// 2. Try pattern matches
		for _, pr := range r.patternRoutes {
			if r.matchesPattern(normalizedDbName, pr) {
				route = pr.RouteConfig
				r.logger.Debug("Found pattern match route",
					zap.String("database", databaseName),
					zap.String("normalized_database", normalizedDbName),
					zap.String("pattern", pr.Pattern),
					zap.String("label", pr.RouteConfig.Label))
				break
			}
		}

		// 3. Try wildcard route
		if route == nil && r.wildcardRoute != nil {
			route = r.wildcardRoute
			r.logger.Debug("Using wildcard route",
				zap.String("database", databaseName),
				zap.String("normalized_database", normalizedDbName),
				zap.String("label", r.wildcardRoute.Label))
		}
	}

	if route == nil {
		err = fmt.Errorf("no route found for database: %s", databaseName)
		return nil, err
	}

	// Cache the result
	r.routeCache.Add(normalizedDbName, route)
	return route, nil
}

// matchesPattern checks if a database name matches a pattern
func (r *DatabaseRouter) matchesPattern(dbName string, pattern *PatternRoute) bool {
	switch pattern.PatternType {
	case PrefixMatch:
		return strings.HasPrefix(dbName, pattern.Pattern)
	case SuffixMatch:
		return strings.HasSuffix(dbName, pattern.Pattern)
	case ContainsMatch:
		return strings.Contains(dbName, pattern.Pattern)
	case WildcardMatch:
		return r.matchesWildcard(dbName, pattern.Pattern)
	default:
		return false
	}
}

// matchesWildcard checks if a database name matches a wildcard pattern
func (r *DatabaseRouter) matchesWildcard(dbName string, pattern string) bool {
	// Convert pattern to regex
	// Replace * with .* for regex matching
	regexPattern := strings.ReplaceAll(pattern, "*", ".*")

	// Add anchors to match the entire string
	regexPattern = "^" + regexPattern + "$"

	// Compile and match
	matched, err := regexp.MatchString(regexPattern, dbName)
	if err != nil {
		r.logger.Error("Error matching wildcard pattern",
			zap.String("pattern", pattern),
			zap.String("regex", regexPattern),
			zap.Error(err))
		return false
	}

	return matched
}

// GetAllRoutes returns all configured routes for monitoring
func (r *DatabaseRouter) GetAllRoutes() map[string]*RouteConfig {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	result := make(map[string]*RouteConfig)

	// Copy exact routes
	for k, v := range r.routes {
		result[k] = v
	}

	// Add pattern routes
	for _, pr := range r.patternRoutes {
		key := pr.Pattern
		switch pr.PatternType {
		case PrefixMatch:
			key = pr.Pattern + "*"
		case SuffixMatch:
			key = "*" + pr.Pattern
		case ContainsMatch:
			key = "*" + pr.Pattern + "*"
		}
		result[key] = pr.RouteConfig
	}

	// Add wildcard route
	if r.wildcardRoute != nil {
		result["*"] = r.wildcardRoute
	}

	return result
}

// ExtractDatabaseName extracts the database name from a MongoDB command
func ExtractDatabaseName(msg *mongo.Message) (string, error) {
	// Get command and collection from the operation
	_, collection := msg.Op.CommandAndCollection()

	// If we have a collection name with database prefix
	if collection != "" {
		collectionInfo := util.ParseCollectionName(collection)
		if collectionInfo.Database != "" {
			return util.NormalizeDatabaseName(collectionInfo.Database), nil
		}
	}

	// For operations targeting admin database
	if msg.Op.IsIsMaster() {
		return util.NormalizeDatabaseName("admin"), nil
	}

	// Default to admin for commands without explicit database
	return util.NormalizeDatabaseName("admin"), nil
}

// RouteMessage routes a message to the appropriate MongoDB instance
func (r *DatabaseRouter) RouteMessage(msg *mongo.Message) (*mongo.Mongo, error) {
	// Extract database name from the message
	dbName, err := ExtractDatabaseName(msg)
	if err != nil {
		// If we can't extract database name, try to use default/wildcard
		if r.wildcardRoute != nil {
			return r.wildcardRoute.MongoClient, nil
		}
		return nil, fmt.Errorf("failed to extract database name: %w", err)
	}

	// Get route for the database
	route, err := r.GetRoute(dbName)
	if err != nil {
		return nil, err
	}

	return route.MongoClient, nil
}

// UpdateRoute updates an existing route configuration
func (r *DatabaseRouter) UpdateRoute(pattern string, config *RouteConfig) error {
	// This is effectively the same as AddRoute
	return r.AddRoute(pattern, config)
}

// Statistics returns routing statistics
func (r *DatabaseRouter) Statistics() map[string]interface{} {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	stats := map[string]interface{}{
		"exact_routes":   len(r.routes),
		"pattern_routes": len(r.patternRoutes),
		"has_wildcard":   r.wildcardRoute != nil,
		"cache_size":     r.routeCache.Len(),
	}

	// Add route details
	routes := make([]map[string]string, 0)
	for db, route := range r.routes {
		routes = append(routes, map[string]string{
			"database":  db,
			"label":     route.Label,
			"pool_mode": route.PoolMode,
		})
	}
	stats["routes"] = routes

	return stats
}

// InitCredentialClientsCache initializes the credential clients cache if not already initialized
func (r *RouteConfig) InitCredentialClientsCache() {
	if r.CredentialClients == nil {
		// Create LRU cache with TTL: max 500 credential clients, 30 minutes TTL
		r.CredentialClients = lruttl.New(200, 30*time.Minute)
	}
}
