package helpers

type key int

const (
	// KeyUser is a key for a user in a context
	KeyUser key = iota
	// KeyToken is a key for a token in a context
	KeyToken
)
