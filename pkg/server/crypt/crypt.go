package crypt

import (
	"crypto/rand"

	"encoding/base64"
	"github.com/pkg/errors"
)

// getRandomBytes generates a cryptographically secure pseudorandom numbers of the
// given size in byte
func getRandomBytes(numBytes int) ([]byte, error) {
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, errors.Wrap(err, "reading random bits")
	}

	return b, nil
}

// GetRandomStr generates a cryptographically secure pseudorandom numbers of the
// given size in byte
func GetRandomStr(numBytes int) (string, error) {
	b, err := getRandomBytes(numBytes)
	if err != nil {
		return "", errors.Wrap(err, "generating random bits")
	}

	return base64.StdEncoding.EncodeToString(b), nil
}
