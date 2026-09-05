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
	memory          = 64 * 1024
	iterations      = 3
	parallelism     = 2
	saltLength      = 16
	keyLength       = 32
	maxEncodedBytes = 1024
	minMemory       = 8 * 1024
	maxMemory       = 256 * 1024
	maxIterations   = 10
	maxParallelism  = 8
	minSaltLength   = 8
	maxSaltLength   = 64
	minKeyLength    = 16
	maxKeyLength    = 64

	dummyCredentialHash = "$argon2id$v=19$m=65536,t=3,p=2$9zUJyMaBuKB6ZV90HRubfg$ITduve02Uwz2SYciVLQio+Kvr2XSQkxJi4/lxqulFts"
)

func Hash(value string) (string, error) {
	if value == "" {
		return "", errors.New("password must not be empty")
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
	parsed, ok := parseCredentialHash(encoded)
	if !ok {
		return false
	}
	actual := argon2.IDKey(
		[]byte(value),
		parsed.salt,
		parsed.iterations,
		parsed.memory,
		parsed.parallelism,
		uint32(len(parsed.expected)),
	)
	return subtle.ConstantTimeCompare(parsed.expected, actual) == 1
}

// ValidHash reports whether encoded is a bounded, structurally valid Argon2id
// credential. It does not verify a password or perform the Argon2 work.
func ValidHash(encoded string) bool {
	_, ok := parseCredentialHash(encoded)
	return ok
}

type parsedCredentialHash struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	expected    []byte
}

func parseCredentialHash(encoded string) (parsedCredentialHash, bool) {
	if len(encoded) == 0 || len(encoded) > maxEncodedBytes {
		return parsedCredentialHash{}, false
	}
	var version int
	var parsedMemory, parsedIterations uint32
	var parsedParallelism uint8
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return parsedCredentialHash{}, false
	}
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return parsedCredentialHash{}, false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &parsedMemory, &parsedIterations, &parsedParallelism); err != nil {
		return parsedCredentialHash{}, false
	}
	if parsedMemory < minMemory ||
		parsedMemory > maxMemory ||
		parsedIterations == 0 ||
		parsedIterations > maxIterations ||
		parsedParallelism == 0 ||
		parsedParallelism > maxParallelism {
		return parsedCredentialHash{}, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < minSaltLength || len(salt) > maxSaltLength {
		return parsedCredentialHash{}, false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < minKeyLength || len(expected) > maxKeyLength {
		return parsedCredentialHash{}, false
	}
	return parsedCredentialHash{
		memory:      parsedMemory,
		iterations:  parsedIterations,
		parallelism: parsedParallelism,
		salt:        salt,
		expected:    expected,
	}, true
}

// VerifyDummy performs the same bounded Argon2 work as a normal password
// check without consulting a persisted credential. Authentication code uses
// it before rejecting unknown or inactive subjects to reduce account
// enumeration through obvious response-time differences.
func VerifyDummy(value string) {
	_ = Verify(dummyCredentialHash, value)
}
