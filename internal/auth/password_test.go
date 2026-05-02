package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPassword_Hashing(t *testing.T) {
	password := "super-secret-123"
	
	hash, err := HashPassword(password)
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$"))

	t.Run("Verify Correct Password", func(t *testing.T) {
		match, err := VerifyPassword(password, hash)
		assert.NoError(t, err)
		assert.True(t, match)
	})

	t.Run("Reject Wrong Password", func(t *testing.T) {
		match, err := VerifyPassword("wrong-password", hash)
		assert.NoError(t, err)
		assert.False(t, match)
	})

	t.Run("Handle Invalid Hash", func(t *testing.T) {
		match, err := VerifyPassword(password, "invalid-hash-format")
		assert.Error(t, err)
		assert.False(t, match)
	})
}

func TestGenerateHumanFriendlyPassword(t *testing.T) {
	pass, err := GenerateHumanFriendlyPassword()
	assert.NoError(t, err)
	assert.Equal(t, 10, len(pass))

	// Check for forbidden characters
	forbidden := "0O1lI"
	for _, char := range forbidden {
		assert.False(t, strings.ContainsRune(pass, char), "Password should not contain %c", char)
	}
}
