package proxy

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/sameer-m-dev/mongobouncer/mongo"
)

// DatabaseRouter handles routing of database connections with wildcard support
type DatabaseRouter struct {
	logger        *zap.Logger
	routes        map[string]*RouteConfig // Exact database name matches
	wildcardRoute *RouteConfig            // Wildcard (*) fallback route
	patternRoutes []*PatternRoute         // Pattern-based routes (e.g., test_*, *_prod)
	mutex         sync.RWMutex
}

// RouteConfig represents configuration for routing to a specific MongoDB instance
type RouteConfig struct {
	DatabaseName     string
	MongoClient      *mongo.Mongo
	ConnectionString string
	PoolMode         string
	MaxConnections   int
	Label            string
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
	return &DatabaseRouter{
		logger:        logger,
		routes:        make(map[string]*RouteConfig),
		patternRoutes: make([]*PatternRoute, 0),
	}
}

// AddRoute adds a database route
func (r *DatabaseRouter) AddRoute(pattern string, config *RouteConfig) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Handle wildcard route
	if pattern == "*" {
		r.wildcardRoute = config
		r.logger.Info("Added wildcard route", zap.String("label", config.Label))
		return nil
	}

	// Handle pattern routes
	if strings.Contains(pattern, "*") {
		patternRoute := &PatternRoute{
			Pattern:     pattern,
			RouteConfig: config,
		}

		// Determine pattern type
		if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
			patternRoute.PatternType = ContainsMatch
			patternRoute.Pattern = strings.Trim(pattern, "*")
		} else if strings.HasPrefix(pattern, "*") {
			patternRoute.PatternType = SuffixMatch
			patternRoute.Pattern = strings.TrimPrefix(pattern, "*")
		} else if strings.HasSuffix(pattern, "*") {
			patternRoute.PatternType = PrefixMatch
			patternRoute.Pattern = strings.TrimSuffix(pattern, "*")
		} else {
			// Handle wildcard patterns with * in the middle
			patternRoute.PatternType = WildcardMatch
			patternRoute.Pattern = pattern
		}

		r.patternRoutes = append(r.patternRoutes, patternRoute)
		r.logger.Info("Added pattern route",
			zap.String("pattern", pattern),
			zap.String("label", config.Label))
		return nil
	}

	// Handle exact match
	r.routes[pattern] = config
	r.logger.Info("Added exact route",
		zap.String("database", pattern),
		zap.String("label", config.Label))
	return nil
}

// RemoveRoute removes a database route
func (r *DatabaseRouter) RemoveRoute(pattern string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

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
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// 1. Try exact match first
	if route, ok := r.routes[databaseName]; ok {
		r.logger.Debug("Found exact match route",
			zap.String("database", databaseName),
			zap.String("label", route.Label))
		return route, nil
	}

	// 2. Try pattern matches
	for _, pr := range r.patternRoutes {
		if r.matchesPattern(databaseName, pr) {
			r.logger.Debug("Found pattern match route",
				zap.String("database", databaseName),
				zap.String("pattern", pr.Pattern),
				zap.String("label", pr.RouteConfig.Label))
			return pr.RouteConfig, nil
		}
	}

	// 3. Try wildcard route
	if r.wildcardRoute != nil {
		r.logger.Debug("Using wildcard route",
			zap.String("database", databaseName),
			zap.String("label", r.wildcardRoute.Label))
		return r.wildcardRoute, nil
	}

	return nil, fmt.Errorf("no route found for database: %s", databaseName)
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
		parts := strings.SplitN(collection, ".", 2)
		if len(parts) > 0 && parts[0] != "" {
			return parts[0], nil
		}
	}

	// For operations targeting admin database
	if msg.Op.IsIsMaster() {
		return "admin", nil
	}

	// Default to admin for commands without explicit database
	return "admin", nil
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
