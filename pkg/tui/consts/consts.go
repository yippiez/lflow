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
	// LflowHomeDirName is the dot-directory under the home dir that holds the
	// user settings file
	LflowHomeDirName = ".lflow"
	// SettingsFilename is the JSON settings file inside LflowHomeDirName
	SettingsFilename = "settings.json"

	// SystemSchema is the key for schema in the system table
	SystemSchema = "schema"
	// SystemLastUpgrade is the timestamp at which the system more recently checked for an upgrade
	SystemLastUpgrade = "last_upgrade"
	// SystemColabAuth holds the Google OAuth token (JSON) for the Colab compute
	// runtime, written by `lflow auth colab`.
	SystemColabAuth = "colab_auth"
)
