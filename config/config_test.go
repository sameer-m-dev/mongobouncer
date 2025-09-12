package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	t.Run("DefaultConfiguration", func(t *testing.T) {
		// Test loading without a config file (should use defaults)
		config, err := LoadConfig("", false)
		assert.NoError(t, err)
		assert.NotNil(t, config)
		assert.NotNil(t, config.Logger())
	})

	t.Run("VerboseFlag", func(t *testing.T) {
		// Test verbose flag overrides log level
		config, err := LoadConfig("", true)
		assert.NoError(t, err)
		assert.NotNil(t, config)
		// Would need to check internal log level, but that's not exposed
	})

	t.Run("MissingConfigFile", func(t *testing.T) {
		// Test error handling for missing config file
		_, err := LoadConfig("nonexistent.toml", false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config file not found")
	})

	t.Run("InvalidTOML", func(t *testing.T) {
		// Create invalid TOML file
		tmpFile := "test_invalid.toml"
		err := os.WriteFile(tmpFile, []byte("[invalid toml content"), 0644)
		require.NoError(t, err)
		defer os.Remove(tmpFile)

		_, err = LoadConfig(tmpFile, false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse config file")
	})

	t.Run("ValidTOMLConfig", func(t *testing.T) {
		// Create valid TOML file
		tmpFile := "test_valid.toml"
		tomlContent := `
[mongobouncer]
listen_port = 27019
log_level = "debug"
ping = true

[databases]
test_db = "mongodb://localhost:27017/test?maxPoolSize=5"
`
		err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
		require.NoError(t, err)
		defer os.Remove(tmpFile)

		config, err := LoadConfig(tmpFile, false)
		assert.NoError(t, err)
		assert.NotNil(t, config)
		assert.NotNil(t, config.Logger())

		// Test that config was loaded
		mainConfig := config.GetMainConfig()
		assert.Equal(t, 27019, mainConfig.ListenPort)
		assert.Equal(t, "debug", mainConfig.LogLevel)
		assert.True(t, mainConfig.Ping)

		// Test databases
		databases := config.GetDatabases()
		assert.Len(t, databases, 1)
		assert.Contains(t, databases, "test_db")
		assert.Equal(t, "mongodb://localhost:27017/test?maxPoolSize=5", databases["test_db"].ConnectionString)
	})

	t.Run("ComplexTOMLConfig", func(t *testing.T) {
		// Test structured database configuration
		tmpFile := "test_complex.toml"
		tomlContent := `
[mongobouncer]
listen_port = 27020
min_pool_size = 10
max_pool_size = 50
max_client_conn = 200

[databases.structured_db]
host = "localhost"
port = 27017
dbname = "testdb"
user = "testuser"
password = "testpass"
maxPoolSize = 15
label = "test_label"

[users.test_user]
password = "hash123"
max_user_connections = 25
`
		err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
		require.NoError(t, err)
		defer os.Remove(tmpFile)

		config, err := LoadConfig(tmpFile, false)
		assert.NoError(t, err)
		assert.NotNil(t, config)

		// Test main config
		mainConfig := config.GetMainConfig()
		assert.Equal(t, 27020, mainConfig.ListenPort)
		assert.Equal(t, 10, mainConfig.MinPoolSize)
		assert.Equal(t, 50, mainConfig.MaxPoolSize)
		assert.Equal(t, 200, mainConfig.MaxClientConn)

		// Test structured database
		databases := config.GetDatabases()
		assert.Len(t, databases, 1)
		assert.Contains(t, databases, "structured_db")

		db := databases["structured_db"]
		assert.Equal(t, "localhost", db.Host)
		assert.Equal(t, 27017, db.Port)
		assert.Equal(t, "testdb", db.DBName)
		assert.Equal(t, "testuser", db.User)
		assert.Equal(t, "testpass", db.Password)
		assert.Equal(t, 15, db.MaxPoolSize)
		assert.Equal(t, "test_label", db.Label)

		// Test users
		users := config.GetUsers()
		assert.Len(t, users, 1)
		assert.Contains(t, users, "test_user")

		user := users["test_user"]
		assert.Equal(t, "hash123", user.Password)
		assert.Equal(t, 25, user.MaxUserConnections)
	})
}

func TestMetricsConfig(t *testing.T) {
	// Test that metrics are properly configured in TOML
	tmpFile := "test_metrics.toml"
	tomlContent := `
[mongobouncer]
metrics_address = "localhost:9090"
metrics_enabled = true

[databases]
test_db = "mongodb://localhost:27017/test"
`
	err := os.WriteFile(tmpFile, []byte(tomlContent), 0644)
	require.NoError(t, err)
	defer os.Remove(tmpFile)

	config, err := LoadConfig(tmpFile, false)
	assert.NoError(t, err)
	assert.NotNil(t, config)

	mainConfig := config.GetMainConfig()
	assert.Equal(t, "localhost:9090", mainConfig.MetricsAddress)
	assert.True(t, mainConfig.MetricsEnabled)
}

func TestSanitizeURI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "NoPassword",
			input:    "mongodb://localhost:27017/test",
			expected: "mongodb://localhost:27017/test",
		},
		{
			name:     "WithPassword",
			input:    "mongodb://user:secret@localhost:27017/test",
			expected: "mongodb://user:***@localhost:27017/test",
		},
		{
			name:     "ComplexURI",
			input:    "mongodb://admin:complexpass123@cluster.mongodb.net:27017/prod?ssl=true",
			expected: "mongodb://admin:***@cluster.mongodb.net:27017/prod?ssl=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeURI(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
