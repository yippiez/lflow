package database

const (
	// TokenTypeResetPassword is a type of a token for reseting password
	TokenTypeResetPassword = "reset_password"
)

const (
	// BookDomainAll incidates that all books are eligible to be the source books
	BookDomainAll = "all"
	// BookDomainIncluding incidates that some specified books are eligible to be the source books
	BookDomainIncluding = "including"
	// BookDomainExluding incidates that all books except for some specified books are eligible to be the source books
	BookDomainExluding = "excluding"
)
