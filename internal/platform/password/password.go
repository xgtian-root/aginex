package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	memory           = 64 * 1024
	iterations       = 3
	parallelism      = 2
	saltLength       = 16
	keyLength        = 32
	maxPasswordBytes = 1024
	maxEncodedBytes  = 1024
	minMemory        = 8 * 1024
	maxMemory        = 256 * 1024
	maxIterations    = 10
	maxParallelism   = 8
	minSaltLength    = 8
	maxSaltLength    = 64
	minKeyLength     = 16
	maxKeyLength     = 64

	dummyCredentialHash = "$argon2id$v=19$m=65536,t=3,p=2$9zUJyMaBuKB6ZV90HRubfg$ITduve02Uwz2SYciVLQio+Kvr2XSQkxJi4/lxqulFts"
)

func Hash(value string) (string, error) {
	if len(value) < 12 {
		return "", errors.New("password must contain at least 12 characters")
	}
	if len(value) > maxPasswordBytes {
		return "", errors.New("password is too long")
	}
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(value), salt, iterations, memory, parallelism, keyLength)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory,
		iterations,
		parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func Verify(encoded, value string) bool {
	if len(encoded) == 0 ||
		len(encoded) > maxEncodedBytes ||
		len(value) > maxPasswordBytes {
		return false
	}
	var version int
	var parsedMemory, parsedIterations uint32
	var parsedParallelism uint8
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &parsedMemory, &parsedIterations, &parsedParallelism); err != nil {
		return false
	}
	if parsedMemory < minMemory ||
		parsedMemory > maxMemory ||
		parsedIterations == 0 ||
		parsedIterations > maxIterations ||
		parsedParallelism == 0 ||
		parsedParallelism > maxParallelism {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < minSaltLength || len(salt) > maxSaltLength {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < minKeyLength || len(expected) > maxKeyLength {
		return false
	}
	actual := argon2.IDKey([]byte(value), salt, parsedIterations, parsedMemory, parsedParallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(expected, actual) == 1
}

// VerifyDummy performs the same bounded Argon2 work as a normal password
// check without consulting a persisted credential. Authentication code uses
// it before rejecting unknown or inactive subjects to reduce account
// enumeration through obvious response-time differences.
func VerifyDummy(value string) {
	if len(value) > maxPasswordBytes {
		value = value[:maxPasswordBytes]
	}
	_ = Verify(dummyCredentialHash, value)
}
