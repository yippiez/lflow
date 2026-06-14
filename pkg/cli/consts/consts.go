// Package consts provides definitions of constants
package consts

var (
	// LegacyDnoteDirName is the name of the legacy directory containing dnote files (for migration)
	LegacyDnoteDirName = ".dnote"
	// LflowDirName is the name of the directory containing lflow files
	LflowDirName = "lflow"
	// LflowDBFileName is a filename for the Lflow SQLite database
	LflowDBFileName = "lflow.db"
	// TmpContentFileBase is the base for the filename for a temporary content
	TmpContentFileBase = "LFLOW_TMPCONTENT"
	// TmpContentFileExt is the extension for the temporary content file
	TmpContentFileExt = "md"
	// ConfigFilename is the name of the config file
	ConfigFilename = "lflowrc"

	// SystemSchema is the key for schema in the system table
	SystemSchema = "schema"
	// SystemRemoteSchema is the key for remote schema in the system table
	SystemRemoteSchema = "remote_schema"
	// SystemLastSyncAt is the timestamp of the server at the last sync
	SystemLastSyncAt = "last_sync_time"
	// SystemLastMaxUSN is the user's max_usn from the server at the alst sync
	SystemLastMaxUSN = "last_max_usn"
	// SystemLastUpgrade is the timestamp at which the system more recently checked for an upgrade
	SystemLastUpgrade = "last_upgrade"
	// SystemSessionKey is the session key
	SystemSessionKey = "session_token"
	// SystemSessionKeyExpiry is the timestamp at which the session key will expire
	SystemSessionKeyExpiry = "session_token_expiry"
)
