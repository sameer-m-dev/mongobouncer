package util

import "strings"

// CollectionInfo represents parsed collection information
type CollectionInfo struct {
	Database   string
	Collection string
	FullName   string
}

// ParseCollectionName parses a collection name into database and collection parts
func ParseCollectionName(fullCollectionName string) *CollectionInfo {
	parts := strings.SplitN(fullCollectionName, ".", 2)

	info := &CollectionInfo{
		FullName: fullCollectionName,
	}

	if len(parts) == 2 {
		info.Database = parts[0]
		info.Collection = parts[1]
	} else if len(parts) == 1 {
		// If no database specified, use empty string
		info.Collection = parts[0]
	}

	return info
}

// ConnectionStringInfo represents parsed connection string information
type ConnectionStringInfo struct {
	Scheme   string
	Username string
	Password string
	Host     string
	Database string
	Options  string
}

// ParseConnectionString parses a MongoDB connection string
func ParseConnectionString(connStr string) *ConnectionStringInfo {
	info := &ConnectionStringInfo{}

	// Split on @ to separate user info from host info
	parts := strings.Split(connStr, "@")
	if len(parts) == 2 {
		// Parse user info
		userInfo := strings.Split(parts[0], "://")[1]
		userParts := strings.Split(userInfo, ":")
		if len(userParts) == 2 {
			info.Username = userParts[0]
			info.Password = userParts[1]
		}

		// Parse host and database
		hostPart := parts[1]
		if strings.Contains(hostPart, "/") {
			hostDbParts := strings.SplitN(hostPart, "/", 2)
			info.Host = hostDbParts[0]
			if len(hostDbParts) > 1 {
				dbOptionsParts := strings.SplitN(hostDbParts[1], "?", 2)
				info.Database = dbOptionsParts[0]
				if len(dbOptionsParts) > 1 {
					info.Options = dbOptionsParts[1]
				}
			}
		} else {
			info.Host = hostPart
		}
	}

	return info
}

// AppNameInfo represents parsed app name information
type AppNameInfo struct {
	Database   string
	Username   string
	Password   string
	AuthSource string
}

// ParseAppName parses app name in database:username:password:authSource format
func ParseAppName(appName string) *AppNameInfo {
	parts := strings.Split(appName, ":")
	info := &AppNameInfo{}

	if len(parts) == 4 {
		// New format: database:username:password:authSource
		info.Database = parts[0]
		info.Username = parts[1]
		info.Password = parts[2]
		info.AuthSource = parts[3]
		// Since mongo has a limit of 128 characters for the appName, we use the database name if the authSource is "use_db"
		// This is a workaround to avoid the appName being too long.
		if parts[3] == "use_db" {
			info.AuthSource = info.Database
		}
	} else if len(parts) == 3 {
		// Old format: database:username:password
		info.Database = parts[0]
		info.Username = parts[1]
		info.Password = parts[2]
		// Default to admin
		info.AuthSource = "admin"
	}

	return info
}
