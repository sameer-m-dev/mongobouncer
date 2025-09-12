package util

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

func GenerateRandomString() string {
	// Generate 16 random bytes (128 bits)
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}

	// Convert to hex string
	hexStr := hex.EncodeToString(b)

	// Add underscores every 8 characters (like a UUID style)
	parts := []string{
		hexStr[0:8],
		hexStr[8:12],
		hexStr[12:16],
		hexStr[16:20],
		hexStr[20:32],
	}

	return strings.Join(parts, "_")
}
