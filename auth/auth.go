package auth

import (
	"bufio"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// AuthType represents the authentication method
type AuthType string

const (
	AuthTypeTrust       AuthType = "trust"
	AuthTypeMD5         AuthType = "md5"
	AuthTypeSCRAMSHA256 AuthType = "scram-sha-256"
	AuthTypeCert        AuthType = "cert"
)

// Manager handles user authentication and authorization
type Manager struct {
	logger      *zap.Logger
	authType    AuthType
	authFile    string
	authQuery   string
	users       map[string]*User
	adminUsers  map[string]bool
	statsUsers  map[string]bool
	fileModTime time.Time
	mutex       sync.RWMutex
}

// User represents a user with authentication information
type User struct {
	Username       string
	PasswordHash   string
	PoolMode       string
	MaxConnections int
	IsAdmin        bool
	IsStats        bool
}

// NewManager creates a new authentication manager
func NewManager(logger *zap.Logger, authType string, authFile string, authQuery string, adminUsers []string, statsUsers []string) (*Manager, error) {
	m := &Manager{
		logger:     logger,
		authType:   AuthType(authType),
		authFile:   authFile,
		authQuery:  authQuery,
		users:      make(map[string]*User),
		adminUsers: make(map[string]bool),
		statsUsers: make(map[string]bool),
	}

	// Build admin/stats user maps
	for _, u := range adminUsers {
		m.adminUsers[u] = true
	}
	for _, u := range statsUsers {
		m.statsUsers[u] = true
	}

	// Load auth file if specified
	if authFile != "" {
		if err := m.loadAuthFile(); err != nil {
			return nil, fmt.Errorf("failed to load auth file: %w", err)
		}
	}

	// Start file watcher if auth file is specified
	if authFile != "" {
		go m.watchAuthFile()
	}

	return m, nil
}

// loadAuthFile loads user authentication from file
func (m *Manager) loadAuthFile() error {
	file, err := os.Open(m.authFile)
	if err != nil {
		return err
	}
	defer file.Close()

	// Get file modification time
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	m.fileModTime = stat.ModTime()

	// Parse users
	users := make(map[string]*User)
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		user, err := m.parseAuthLine(line)
		if err != nil {
			m.logger.Warn("Failed to parse auth file line",
				zap.Int("line", lineNum),
				zap.String("content", line),
				zap.Error(err))
			continue
		}

		// Check if user is admin or stats
		if m.adminUsers[user.Username] {
			user.IsAdmin = true
		}
		if m.statsUsers[user.Username] {
			user.IsStats = true
		}

		users[user.Username] = user
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	m.mutex.Lock()
	m.users = users
	m.mutex.Unlock()

	m.logger.Info("Loaded auth file",
		zap.String("file", m.authFile),
		zap.Int("users", len(users)))

	return nil
}

// parseAuthLine parses a line from the auth file
// Format: "username" "password_hash" "extra_info"
func (m *Manager) parseAuthLine(line string) (*User, error) {
	// Check if line starts with quotes (required format)
	if !strings.HasPrefix(line, `"`) {
		return nil, errors.New("auth line must start with quoted username")
	}

	// Simple parser for quoted strings
	parts := make([]string, 0, 3)
	inQuote := false
	current := strings.Builder{}

	for i, ch := range line {
		switch ch {
		case '"':
			if inQuote {
				// End of quoted string
				parts = append(parts, current.String())
				current.Reset()
				inQuote = false
			} else {
				// Start of quoted string
				inQuote = true
			}
		default:
			if inQuote {
				current.WriteRune(ch)
			} else if ch != ' ' && ch != '\t' {
				// Non-whitespace outside quotes - could be extra info
				if i < len(line)-1 {
					// Parse rest of line as extra info
					extraInfo := strings.TrimSpace(line[i:])
					if extraInfo != "" {
						parts = append(parts, extraInfo)
					}
					break
				}
			}
		}
	}

	if len(parts) < 2 {
		return nil, errors.New("invalid auth line format")
	}

	// Validate that we have proper quoted strings
	if parts[0] == "" || parts[1] == "" {
		return nil, errors.New("username and password cannot be empty")
	}

	user := &User{
		Username:     parts[0],
		PasswordHash: parts[1],
	}

	// Parse extra info if present
	if len(parts) > 2 {
		m.parseExtraInfo(user, parts[2])
	}

	return user, nil
}

// parseExtraInfo parses extra user information
func (m *Manager) parseExtraInfo(user *User, info string) {
	// Parse key=value pairs
	pairs := strings.Fields(info)
	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}

		key, value := kv[0], kv[1]
		switch key {
		case "pool_mode":
			user.PoolMode = value
		case "max_connections":
			// Parse int, ignore errors
			fmt.Sscanf(value, "%d", &user.MaxConnections)
		case "stats_only":
			if value == "true" {
				user.IsStats = true
			}
		}
	}
}

// watchAuthFile watches for changes to the auth file
func (m *Manager) watchAuthFile() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stat, err := os.Stat(m.authFile)
		if err != nil {
			m.logger.Error("Failed to stat auth file", zap.Error(err))
			continue
		}

		if stat.ModTime().After(m.fileModTime) {
			m.logger.Info("Auth file changed, reloading")
			if err := m.loadAuthFile(); err != nil {
				m.logger.Error("Failed to reload auth file", zap.Error(err))
			}
		}
	}
}

// Authenticate verifies user credentials
func (m *Manager) Authenticate(username, password string) error {
	// Trust authentication - accept all
	if m.authType == AuthTypeTrust {
		return nil
	}

	m.mutex.RLock()
	user, exists := m.users[username]
	m.mutex.RUnlock()

	if !exists {
		return errors.New("authentication failed: user not found")
	}

	// Verify password based on auth type
	switch m.authType {
	case AuthTypeMD5:
		return m.verifyMD5(username, password, user.PasswordHash)
	case AuthTypeSCRAMSHA256:
		return m.verifySCRAMSHA256(username, password, user.PasswordHash)
	default:
		return fmt.Errorf("unsupported auth type: %s", m.authType)
	}
}

// verifyMD5 verifies MD5 password hash
func (m *Manager) verifyMD5(username, password, storedHash string) error {
	// pgBouncer style: md5(password + username)
	h := md5.New()
	h.Write([]byte(password + username))
	hash := hex.EncodeToString(h.Sum(nil))

	// Remove "md5" prefix if present
	if strings.HasPrefix(storedHash, "md5") {
		storedHash = storedHash[3:]
	}

	if hash != storedHash {
		return errors.New("authentication failed: invalid password")
	}

	return nil
}

// verifySCRAMSHA256 verifies SCRAM-SHA-256 password
func (m *Manager) verifySCRAMSHA256(username, password, storedHash string) error {
	// This is a simplified version - real SCRAM-SHA-256 is more complex
	// In production, you'd use the MongoDB driver's SCRAM implementation

	// For now, we'll do a simple SHA-256 comparison
	h := sha256.New()
	h.Write([]byte(password))
	hash := hex.EncodeToString(h.Sum(nil))

	if hash != storedHash {
		return errors.New("authentication failed: invalid password")
	}

	return nil
}

// GetUser returns user information
func (m *Manager) GetUser(username string) (*User, bool) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	user, exists := m.users[username]
	if !exists {
		// Check if user is in admin/stats lists even if not in auth file
		if m.adminUsers[username] || m.statsUsers[username] {
			return &User{
				Username: username,
				IsAdmin:  m.adminUsers[username],
				IsStats:  m.statsUsers[username],
			}, true
		}
		return nil, false
	}

	return user, true
}

// IsAdminUser checks if a user is an admin
func (m *Manager) IsAdminUser(username string) bool {
	if m.adminUsers[username] {
		return true
	}

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if user, exists := m.users[username]; exists {
		return user.IsAdmin
	}

	return false
}

// IsStatsUser checks if a user can view statistics
func (m *Manager) IsStatsUser(username string) bool {
	if m.IsAdminUser(username) {
		return true
	}

	if m.statsUsers[username] {
		return true
	}

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if user, exists := m.users[username]; exists {
		return user.IsStats
	}

	return false
}

// GetPoolMode returns the pool mode for a user
func (m *Manager) GetPoolMode(username string) string {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if user, exists := m.users[username]; exists && user.PoolMode != "" {
		return user.PoolMode
	}

	return "" // Use default
}

// GetMaxConnections returns the max connections for a user
func (m *Manager) GetMaxConnections(username string) int {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if user, exists := m.users[username]; exists && user.MaxConnections > 0 {
		return user.MaxConnections
	}

	return 0 // Use default
}
