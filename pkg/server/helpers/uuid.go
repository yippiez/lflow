package helpers

import (
	"github.com/google/uuid"
	"github.com/pkg/errors"
)

// GenUUID generates a new uuid v4
func GenUUID() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", errors.Wrap(err, "generating uuid")
	}

	return id.String(), nil
}

// ValidateUUID validates the given uuid
func ValidateUUID(u string) bool {
	_, err := uuid.Parse(u)

	return err == nil
}
