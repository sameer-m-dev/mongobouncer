package auth

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAuthManagerIntegration(t *testing.T) {
	logger := zap.NewNop()

	t.Run("CompleteAuthWorkflow", func(t *testing.T) {
		// Create temp directory
		tmpDir, err := ioutil.TempDir("", "auth-integration")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Create auth file with various users
		authFile := filepath.Join(tmpDir, "users.txt")
		authContent := createTestAuthFile(t, []testUser{
			{username: "admin", password: "adminpass", extra: "stats_only=false"},
			{username: "app_user", password: "apppass", extra: "pool_mode=transaction max_connections=50"},
			{username: "readonly", password: "readpass", extra: "pool_mode=statement max_connections=100"},
			{username: "monitor", password: "monitorpass", extra: "stats_only=true"},
		})
		err = ioutil.WriteFile(authFile, []byte(authContent), 0644)
		require.NoError(t, err)

		// Create manager
		m, err := NewManager(logger, "md5", authFile, "",
			[]string{"admin", "superuser"},
			[]string{"monitor", "stats"})
		assert.NoError(t, err)

		// Test authentication
		testCases := []struct {
			username   string
			password   string
			shouldPass bool
		}{
			{"admin", "adminpass", true},
			{"admin", "wrongpass", false},
			{"app_user", "apppass", true},
			{"readonly", "readpass", true},
			{"monitor", "monitorpass", true},
			{"nonexistent", "anypass", false},
			{"", "", false},
		}

		for _, tc := range testCases {
			err := m.Authenticate(tc.username, tc.password)
			if tc.shouldPass {
				assert.NoError(t, err, "User %s should authenticate", tc.username)
			} else {
				assert.Error(t, err, "User %s should not authenticate", tc.username)
			}
		}

		// Test user properties
		user, exists := m.GetUser("app_user")
		assert.True(t, exists)
		assert.Equal(t, "transaction", user.PoolMode)
		assert.Equal(t, 50, user.MaxConnections)

		user, exists = m.GetUser("readonly")
		assert.True(t, exists)
		assert.Equal(t, "statement", user.PoolMode)
		assert.Equal(t, 100, user.MaxConnections)

		// Test permissions
		assert.True(t, m.IsAdminUser("admin"))
		assert.True(t, m.IsAdminUser("superuser")) // From admin list
		assert.False(t, m.IsAdminUser("app_user"))

		assert.True(t, m.IsStatsUser("admin")) // Admins can see stats
		assert.True(t, m.IsStatsUser("monitor"))
		assert.True(t, m.IsStatsUser("stats")) // From stats list
		assert.False(t, m.IsStatsUser("app_user"))
	})

	t.Run("DynamicAuthFileReload", func(t *testing.T) {
		// Create temp directory
		tmpDir, err := ioutil.TempDir("", "auth-reload")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Initial auth file
		authFile := filepath.Join(tmpDir, "users.txt")
		authContent := createTestAuthFile(t, []testUser{
			{username: "user1", password: "pass1", extra: ""},
		})
		err = ioutil.WriteFile(authFile, []byte(authContent), 0644)
		require.NoError(t, err)

		// Create manager with short watch interval
		m, err := NewManager(logger, "md5", authFile, "", nil, nil)
		assert.NoError(t, err)

		// Verify initial state
		err = m.Authenticate("user1", "pass1")
		assert.NoError(t, err)
		err = m.Authenticate("user2", "pass2")
		assert.Error(t, err)

		// Update auth file
		time.Sleep(100 * time.Millisecond) // Ensure different mod time
		authContent = createTestAuthFile(t, []testUser{
			{username: "user1", password: "newpass1", extra: ""},
			{username: "user2", password: "pass2", extra: "pool_mode=transaction"},
		})
		err = ioutil.WriteFile(authFile, []byte(authContent), 0644)
		require.NoError(t, err)

		// Manually trigger reload
		err = m.loadAuthFile()
		assert.NoError(t, err)

		// Verify changes
		err = m.Authenticate("user1", "pass1")
		assert.Error(t, err) // Old password should fail
		err = m.Authenticate("user1", "newpass1")
		assert.NoError(t, err) // New password should work
		err = m.Authenticate("user2", "pass2")
		assert.NoError(t, err) // New user should work

		// Check new user properties
		user, exists := m.GetUser("user2")
		assert.True(t, exists)
		assert.Equal(t, "transaction", user.PoolMode)
	})

	t.Run("SCRAMAuthentication", func(t *testing.T) {
		// Create temp directory
		tmpDir, err := ioutil.TempDir("", "auth-scram")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Create auth file with SCRAM hashes
		authFile := filepath.Join(tmpDir, "users.txt")

		// Generate SCRAM-SHA-256 hash (simplified for testing)
		h := sha256.New()
		h.Write([]byte("scrampass"))
		scramHash := hex.EncodeToString(h.Sum(nil))

		authContent := `"scramuser" "` + scramHash + `" "pool_mode=session"`
		err = ioutil.WriteFile(authFile, []byte(authContent), 0644)
		require.NoError(t, err)

		// Create manager with SCRAM auth
		m, err := NewManager(logger, "scram-sha-256", authFile, "", nil, nil)
		assert.NoError(t, err)

		// Test authentication
		err = m.Authenticate("scramuser", "scrampass")
		assert.NoError(t, err)

		err = m.Authenticate("scramuser", "wrongpass")
		assert.Error(t, err)
	})

	t.Run("TrustAuthentication", func(t *testing.T) {
		// Trust auth accepts all connections
		m, err := NewManager(logger, "trust", "", "", nil, nil)
		assert.NoError(t, err)

		// Any username/password should work
		err = m.Authenticate("anyone", "anypassword")
		assert.NoError(t, err)

		err = m.Authenticate("", "")
		assert.NoError(t, err)
	})

	t.Run("ConcurrentAccess", func(t *testing.T) {
		// Create temp directory
		tmpDir, err := ioutil.TempDir("", "auth-concurrent")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		// Create auth file
		authFile := filepath.Join(tmpDir, "users.txt")
		authContent := createTestAuthFile(t, []testUser{
			{username: "concurrent1", password: "pass1", extra: ""},
			{username: "concurrent2", password: "pass2", extra: ""},
			{username: "concurrent3", password: "pass3", extra: ""},
		})
		err = ioutil.WriteFile(authFile, []byte(authContent), 0644)
		require.NoError(t, err)

		m, err := NewManager(logger, "md5", authFile, "", nil, nil)
		assert.NoError(t, err)

		// Concurrent authentication attempts
		var wg sync.WaitGroup
		errors := make(chan error, 100)

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					user := fmt.Sprintf("concurrent%d", (j%3)+1)
					pass := fmt.Sprintf("pass%d", (j%3)+1)

					if err := m.Authenticate(user, pass); err != nil {
						errors <- err
					}

					// Also test user lookups
					if _, exists := m.GetUser(user); !exists {
						errors <- fmt.Errorf("user %s not found", user)
					}
				}
			}(i)
		}

		// Concurrent file reloads
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				time.Sleep(10 * time.Millisecond)
				m.loadAuthFile()
			}()
		}

		wg.Wait()
		close(errors)

		// Check for errors
		errorCount := 0
		for err := range errors {
			t.Logf("Concurrent error: %v", err)
			errorCount++
		}
		assert.Equal(t, 0, errorCount, "Should have no errors in concurrent access")
	})

	t.Run("MalformedAuthFile", func(t *testing.T) {
		// Create temp directory
		tmpDir, err := ioutil.TempDir("", "auth-malformed")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		authFile := filepath.Join(tmpDir, "malformed.txt")

		testCases := []struct {
			name    string
			content string
			users   int // Expected number of valid users
		}{
			{
				name: "MixedValidInvalid",
				content: `# Comment line
"validuser" "validhash"
invalid line without quotes
"anotheruser" "anotherhash" "pool_mode=transaction"
"missing_password"
"" "emptyhash"
`,
				users: 2, // Only validuser and anotheruser
			},
			{
				name:    "EmptyFile",
				content: ``,
				users:   0,
			},
			{
				name: "OnlyComments",
				content: `# Comment 1
# Comment 2
# Comment 3`,
				users: 0,
			},
			{
				name:    "ExtraQuotes",
				content: `"user1" "hash1" "extra" "more" "stuff"`,
				users:   1,
			},
			{
				name: "SpecialCharacters",
				content: `"user@domain" "hash1"
"user-with-dash" "hash2"
"user.with.dot" "hash3"
"user_underscore" "hash4"
"user$dollar" "hash5"`,
				users: 5,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				err := ioutil.WriteFile(authFile, []byte(tc.content), 0644)
				require.NoError(t, err)

				m, err := NewManager(logger, "md5", authFile, "", nil, nil)
				assert.NoError(t, err)
				assert.Len(t, m.users, tc.users)
			})
		}
	})

	t.Run("AuthQuery", func(t *testing.T) {
		// Test with auth query (would need MongoDB connection in real scenario)
		m, err := NewManager(logger, "md5", "", "db.users.find({username: ?})", nil, nil)
		assert.NoError(t, err)
		assert.Equal(t, "db.users.find({username: ?})", m.authQuery)

		// In real implementation, this would query MongoDB
		// For now, just verify the query was stored
	})

	t.Run("PoolModeInheritance", func(t *testing.T) {
		// Create temp directory
		tmpDir, err := ioutil.TempDir("", "auth-poolmode")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		authFile := filepath.Join(tmpDir, "users.txt")
		authContent := `"default_user" "hash1"
"transaction_user" "hash2" "pool_mode=transaction"
"statement_user" "hash3" "pool_mode=statement max_connections=200"
`
		err = ioutil.WriteFile(authFile, []byte(authContent), 0644)
		require.NoError(t, err)

		m, err := NewManager(logger, "md5", authFile, "", nil, nil)
		assert.NoError(t, err)

		// Test pool modes
		assert.Equal(t, "", m.GetPoolMode("default_user")) // No override
		assert.Equal(t, "transaction", m.GetPoolMode("transaction_user"))
		assert.Equal(t, "statement", m.GetPoolMode("statement_user"))
		assert.Equal(t, "", m.GetPoolMode("nonexistent"))

		// Test max connections
		assert.Equal(t, 0, m.GetMaxConnections("default_user")) // No override
		assert.Equal(t, 0, m.GetMaxConnections("transaction_user"))
		assert.Equal(t, 200, m.GetMaxConnections("statement_user"))
	})
}

// Helper to create test auth file content
type testUser struct {
	username string
	password string
	extra    string
}

func createTestAuthFile(t *testing.T, users []testUser) string {
	content := "# Test auth file\n"
	for _, user := range users {
		// Generate MD5 hash
		h := md5.New()
		h.Write([]byte(user.password + user.username))
		hash := "md5" + hex.EncodeToString(h.Sum(nil))

		content += fmt.Sprintf(`"%s" "%s"`, user.username, hash)
		if user.extra != "" {
			content += fmt.Sprintf(` "%s"`, user.extra)
		}
		content += "\n"
	}
	return content
}
