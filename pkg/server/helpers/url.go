package helpers

import (
	"fmt"
	"net/url"
)

// GetPath returns a path optionally suffixed by query string
func GetPath(path string, query *url.Values) string {
	if query == nil {
		return path
	}

	q := query.Encode()
	return fmt.Sprintf("%s?%s", path, q)
}
