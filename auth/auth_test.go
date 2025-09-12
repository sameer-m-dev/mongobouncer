package auth

import (
	"crypto/md5"
	"encoding/hex"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestAuthManager(t *testing.T) {
	logger := zap.NewNop()

	t.Run("TrustAuth", func(t *testing.T) {
		m, err := NewManager(logger, "trust", "", "", nil, nil)
		assert.NoError(t, err)

		// Trust auth accepts all
		err = m.Authenticate("anyuser", "anypass")
		assert.NoError(t, err)
	})

	t.Run("MD5Auth", func(t *testing.T) {
		// Create temp auth file
		tmpDir, err := ioutil.TempDir("", "auth-test")
		assert.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		authFile := filepath.Join(tmpDir, "users.txt")

		// Calculate MD5 hash for testuser/testpass
		h := md5.New()
		h.Write([]byte("testpass" + "testuser"))
		hash := hex.EncodeToString(h.Sum(nil))

		content := `# Test auth file
"testuser" "md5` + hash + `" "pool_mode=transaction max_connections=50"
"admin" "md5adminpasshash" "stats_only=true"
`
		err = ioutil.WriteFile(authFile, []byte(content), 0644)
		assert.NoError(t, err)

		m, err := NewManager(logger, "md5", authFile, "", []string{"admin"}, []string{"stats"})
		assert.NoError(t, err)

		// Test valid authentication
		err = m.Authenticate("testuser", "testpass")
		assert.NoError(t, err)

		// Test invalid password
		err = m.Authenticate("testuser", "wrongpass")
		assert.Error(t, err)

		// Test non-existent user
		err = m.Authenticate("nouser", "pass")
		assert.Error(t, err)
	})

	t.Run("ParseAuthLine", func(t *testing.T) {
		m, _ := NewManager(logger, "trust", "", "", nil, nil)

		tests := []struct {
			line     string
			expected *User
			wantErr  bool
		}{
			{
				line: `"user1" "hash1"`,
				expected: &User{
					Username:     "user1",
					PasswordHash: "hash1",
				},
			},
			{
				line: `"user2" "hash2" "pool_mode=session max_connections=100"`,
				expected: &User{
					Username:       "user2",
					PasswordHash:   "hash2",
					PoolMode:       "session",
					MaxConnections: 100,
				},
			},
			{
				line: `"user3" "hash3" "stats_only=true"`,
				expected: &User{
					Username:     "user3",
					PasswordHash: "hash3",
					IsStats:      true,
				},
			},
			{
				line:    `"user4"`, // Missing password
				wantErr: true,
			},
			{
				line:    ``, // Empty line
				wantErr: true,
			},
		}

		for _, tt := range tests {
			user, err := m.parseAuthLine(tt.line)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected.Username, user.Username)
				assert.Equal(t, tt.expected.PasswordHash, user.PasswordHash)
				assert.Equal(t, tt.expected.PoolMode, user.PoolMode)
				assert.Equal(t, tt.expected.MaxConnections, user.MaxConnections)
				assert.Equal(t, tt.expected.IsStats, user.IsStats)
			}
		}
	})

	t.Run("UserManagement", func(t *testing.T) {
		// Create temp auth file
		tmpDir, err := ioutil.TempDir("", "auth-test")
		assert.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		authFile := filepath.Join(tmpDir, "users.txt")
		content := `"user1" "hash1"
"user2" "hash2" "pool_mode=transaction"
`
		err = ioutil.WriteFile(authFile, []byte(content), 0644)
		assert.NoError(t, err)

		m, err := NewManager(logger, "trust", authFile, "", []string{"admin"}, []string{"user2"})
		assert.NoError(t, err)

		// Test GetUser
		user, exists := m.GetUser("user1")
		assert.True(t, exists)
		assert.Equal(t, "user1", user.Username)

		user, exists = m.GetUser("user2")
		assert.True(t, exists)
		assert.Equal(t, "transaction", user.PoolMode)

		// Test admin/stats users
		assert.True(t, m.IsAdminUser("admin"))
		assert.False(t, m.IsAdminUser("user1"))
		
		assert.True(t, m.IsStatsUser("admin")) // Admins are also stats users
		assert.True(t, m.IsStatsUser("user2"))
		assert.False(t, m.IsStatsUser("user1"))

		// Test pool mode
		assert.Equal(t, "transaction", m.GetPoolMode("user2"))
		assert.Equal(t, "", m.GetPoolMode("user1"))
	})

	t.Run("FileReload", func(t *testing.T) {
		// Create temp auth file
		tmpDir, err := ioutil.TempDir("", "auth-test")
		assert.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		authFile := filepath.Join(tmpDir, "users.txt")
		content := `"user1" "hash1"`
		err = ioutil.WriteFile(authFile, []byte(content), 0644)
		assert.NoError(t, err)

		m, err := NewManager(logger, "trust", authFile, "", nil, nil)
		assert.NoError(t, err)

		// Verify initial user
		user, exists := m.GetUser("user1")
		assert.True(t, exists)
		assert.Equal(t, "user1", user.Username)

		// Update file
		newContent := `"user1" "newhash"
"user2" "hash2"`
		err = ioutil.WriteFile(authFile, []byte(newContent), 0644)
		assert.NoError(t, err)

		// Reload manually
		err = m.loadAuthFile()
		assert.NoError(t, err)

		// Verify changes
		user, exists = m.GetUser("user1")
		assert.True(t, exists)
		assert.Equal(t, "newhash", user.PasswordHash)

		user, exists = m.GetUser("user2")
		assert.True(t, exists)
		assert.Equal(t, "user2", user.Username)
	})
}