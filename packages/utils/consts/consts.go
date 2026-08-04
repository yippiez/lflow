// Package consts provides definitions of constants
package consts

var (
	// LflowDirName is the name of the directory containing lflow files
	LflowDirName = "lflow"
	// LflowDBFileName is a filename for the Lflow SQLite database
	LflowDBFileName = "lflow.db"
	// LflowHomeDirName is the dot-directory under the home dir that holds the
	// user settings file
	LflowHomeDirName = ".lflow"
	// SettingsFilename is the JSON settings file inside LflowHomeDirName
	SettingsFilename = "settings.json"

	// SystemLastUpgrade is the timestamp at which the system more recently checked for an upgrade
	SystemLastUpgrade = "last_upgrade"
)
