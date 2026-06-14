package sync

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/cli/consts"
	cliDatabase "github.com/lflow/lflow/pkg/cli/database"
	clitest "github.com/lflow/lflow/pkg/cli/testutils"
	"github.com/lflow/lflow/pkg/server/app"
	"github.com/lflow/lflow/pkg/server/controllers"
	"github.com/lflow/lflow/pkg/server/database"
	apitest "github.com/lflow/lflow/pkg/server/testutils"
	"github.com/lflow/lflow/pkg/shared/assert"
	"github.com/lflow/lflow/pkg/shared/clock"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// testEnv holds the test environment for a single test
type testEnv struct {
	DB       *cliDatabase.DB
	CmdOpts  clitest.RunDnoteCmdOptions
	Server   *httptest.Server
	ServerDB *gorm.DB
	TmpDir   string
}

// setupTestEnv creates an isolated test environment with its own database and temp directory
func setupTestEnv(t *testing.T) testEnv {
	tmpDir := t.TempDir()

	// Create lflow directory
	lflowDir := filepath.Join(tmpDir, consts.LflowDirName)
	if err := os.MkdirAll(lflowDir, 0755); err != nil {
		t.Fatal(errors.Wrap(err, "creating lflow directory"))
	}

	// Create database at the expected path
	dbPath := filepath.Join(lflowDir, consts.LflowDBFileName)
	db := cliDatabase.InitTestFileDBRaw(t, dbPath)

	// Create server
	server, serverDB := setupNewServer(t)

	// Create config file with this server's endpoint
	apiEndpoint := fmt.Sprintf("%s/api", server.URL)
	updateConfigAPIEndpoint(t, tmpDir, apiEndpoint)

	// Create command options with HOME and XDG paths pointing to temp dir.
	// HOME isolates ~/.lflow/settings.json to the test dir.
	cmdOpts := clitest.RunDnoteCmdOptions{
		Env: []string{
			fmt.Sprintf("HOME=%s", tmpDir),
			fmt.Sprintf("XDG_CONFIG_HOME=%s", tmpDir),
			fmt.Sprintf("XDG_DATA_HOME=%s", tmpDir),
			fmt.Sprintf("XDG_CACHE_HOME=%s", tmpDir),
		},
	}

	return testEnv{
		DB:       db,
		CmdOpts:  cmdOpts,
		Server:   server,
		ServerDB: serverDB,
		TmpDir:   tmpDir,
	}
}

// setupTestServer creates a test server with its own database
func setupTestServer(t *testing.T, serverTime time.Time) (*httptest.Server, *gorm.DB, error) {
	db := apitest.InitMemoryDB(t)

	mockClock := clock.NewMock()
	mockClock.SetNow(serverTime)

	a := app.NewTest()
	a.Clock = mockClock
	a.EmailBackend = &apitest.MockEmailbackendImplementation{}
	a.DB = db

	server, err := controllers.NewServer(&a)
	if err != nil {
		return nil, nil, errors.Wrap(err, "initializing server")
	}

	return server, db, nil
}

// setupNewServer creates a new server and returns the server and database.
// This is useful when a test needs to switch to a new empty server.
func setupNewServer(t *testing.T) (*httptest.Server, *gorm.DB) {
	server, serverDB, err := setupTestServer(t, serverTime)
	if err != nil {
		t.Fatal(errors.Wrap(err, "setting up new test server"))
	}
	t.Cleanup(func() { server.Close() })

	return server, serverDB
}

// updateConfigAPIEndpoint writes the JSON settings file with the given API
// endpoint, at ~/.lflow/settings.json relative to the test's HOME.
func updateConfigAPIEndpoint(t *testing.T, tmpDir string, apiEndpoint string) {
	lflowDir := filepath.Join(tmpDir, consts.LflowHomeDirName)
	if err := os.MkdirAll(lflowDir, 0755); err != nil {
		t.Fatal(errors.Wrap(err, "creating settings directory"))
	}
	configPath := filepath.Join(lflowDir, consts.SettingsFilename)
	configContent := fmt.Sprintf("{\n  \"apiEndpoint\": %q\n}\n", apiEndpoint)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(errors.Wrap(err, "writing settings file"))
	}
}

// switchToEmptyServer closes the current server and creates a new empty server,
// updating the config file to point to it.
func switchToEmptyServer(t *testing.T, env *testEnv) {
	// Close old server
	env.Server.Close()

	// Create new empty server
	env.Server, env.ServerDB = setupNewServer(t)

	// Update config file to point to new server
	apiEndpoint := fmt.Sprintf("%s/api", env.Server.URL)
	updateConfigAPIEndpoint(t, env.TmpDir, apiEndpoint)
}

// setupUser creates a test user in the server database
func setupUser(t *testing.T, env testEnv) database.User {
	user := apitest.SetupUserData(env.ServerDB, "alice@example.com", "pass1234")

	return user
}

// setupUserAndLogin creates a test user and logs them in on the CLI
func setupUserAndLogin(t *testing.T, env testEnv) database.User {
	user := setupUser(t, env)
	login(t, env.DB, env.ServerDB, user)

	return user
}

// login logs in the user in CLI
func login(t *testing.T, db *cliDatabase.DB, serverDB *gorm.DB, user database.User) {
	session := apitest.SetupSession(serverDB, user)

	cliDatabase.MustExec(t, "inserting session_key", db, "INSERT INTO system (key, value) VALUES (?, ?)", consts.SystemSessionKey, session.Key)
	cliDatabase.MustExec(t, "inserting session_key_expiry", db, "INSERT INTO system (key, value) VALUES (?, ?)", consts.SystemSessionKeyExpiry, session.ExpiresAt.Unix())
}

// nodeJSON builds a JSON payload for node create/update.
func nodeJSON(parentUUID string, rank int, name string) string {
	return fmt.Sprintf(`{"parent_uuid": "%s", "rank": %d, "name": "%s", "layout": "bullets"}`, parentUUID, rank, name)
}

// apiCreateNode creates a node via the API and returns its UUID
func apiCreateNode(t *testing.T, env testEnv, user database.User, parentUUID string, rank int, name, message string) string {
	res := doHTTPReq(t, env, "POST", "/v3/nodes", nodeJSON(parentUUID, rank, name), message, user)

	var resp controllers.CreateNodeResp
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatal(errors.Wrap(err, "decoding payload for adding node"))
		return ""
	}

	return resp.Result.UUID
}

// apiPatchNode updates a node via the API
func apiPatchNode(t *testing.T, env testEnv, user database.User, nodeUUID, payload, message string) {
	doHTTPReq(t, env, "PATCH", fmt.Sprintf("/v3/nodes/%s", nodeUUID), payload, message, user)
}

// apiDeleteNode deletes a node via the API
func apiDeleteNode(t *testing.T, env testEnv, user database.User, nodeUUID, message string) {
	doHTTPReq(t, env, "DELETE", fmt.Sprintf("/v3/nodes/%s", nodeUUID), "", message, user)
}

// doHTTPReq performs an authenticated HTTP request and checks for errors
func doHTTPReq(t *testing.T, env testEnv, method, path, payload, message string, user database.User) *http.Response {
	apiEndpoint := fmt.Sprintf("%s/api", env.Server.URL)
	endpoint := fmt.Sprintf("%s%s", apiEndpoint, path)

	req, err := http.NewRequest(method, endpoint, strings.NewReader(payload))
	if err != nil {
		panic(errors.Wrap(err, "constructing http request"))
	}

	res := apitest.HTTPAuthDo(t, env.ServerDB, req, user)
	if res.StatusCode >= 400 {
		bs, err := io.ReadAll(res.Body)
		if err != nil {
			panic(errors.Wrap(err, "parsing response body for error"))
		}

		t.Errorf("%s. HTTP status %d. Message: %s", message, res.StatusCode, string(bs))
	}

	return res
}

// setupFunc is a function that sets up test data and returns IDs for assertions
type setupFunc func(t *testing.T, env testEnv, user database.User) map[string]string

// assertFunc is a function that asserts the expected state after sync
type assertFunc func(t *testing.T, env testEnv, user database.User, ids map[string]string)

// testSyncCmd is a test helper that sets up a test environment, runs setup, syncs, and asserts
func testSyncCmd(t *testing.T, fullSync bool, setup setupFunc, assert assertFunc) {
	env := setupTestEnv(t)

	user := setupUserAndLogin(t, env)
	ids := setup(t, env, user)

	if fullSync {
		clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "server", "sync", "-f")
	} else {
		clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "server", "sync")
	}

	assert(t, env, user, ids)
}

// systemState represents the expected state of the sync system
type systemState struct {
	clientNodeCount  int
	clientLastMaxUSN int
	clientLastSyncAt int64
	serverNodeCount  int64
	serverUserMaxUSN int
}

// checkState compares the state of the client and the server with the given system state
func checkState(t *testing.T, clientDB *cliDatabase.DB, user database.User, serverDB *gorm.DB, expected systemState) {
	var clientNodeCount int
	cliDatabase.MustScan(t, "counting client nodes", clientDB.QueryRow("SELECT count(*) FROM nodes WHERE uuid != 'root'"), &clientNodeCount)
	assert.Equal(t, clientNodeCount, expected.clientNodeCount, "client node count mismatch")

	var clientLastMaxUSN int
	var clientLastSyncAt int64
	cliDatabase.MustScan(t, "finding system last_max_usn", clientDB.QueryRow("SELECT value FROM system WHERE key = ?", consts.SystemLastMaxUSN), &clientLastMaxUSN)
	cliDatabase.MustScan(t, "finding system last_sync_at", clientDB.QueryRow("SELECT value FROM system WHERE key = ?", consts.SystemLastSyncAt), &clientLastSyncAt)
	assert.Equal(t, clientLastMaxUSN, expected.clientLastMaxUSN, "client last_max_usn mismatch")
	assert.Equal(t, clientLastSyncAt, expected.clientLastSyncAt, "client last_sync_at mismatch")

	var serverNodeCount int64
	apitest.MustExec(t, serverDB.Model(&database.Node{}).Count(&serverNodeCount), "counting server nodes")
	assert.Equal(t, serverNodeCount, expected.serverNodeCount, "server node count mismatch")
	var serverUser database.User
	apitest.MustExec(t, serverDB.Where("id = ?", user.ID).First(&serverUser), "finding user")
	assert.Equal(t, serverUser.MaxUSN, expected.serverUserMaxUSN, "user max_usn mismatch")

}
