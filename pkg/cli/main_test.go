/* Copyright 2025 Lflow Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/assert"
	"github.com/lflow/lflow/pkg/cli/consts"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/testutils"
	"github.com/lflow/lflow/pkg/cli/utils"
	"github.com/pkg/errors"
)

var binaryName = "test-lflow"

// setupTestEnv creates a unique test directory for parallel test execution
func setupTestEnv(t *testing.T) (string, testutils.RunDnoteCmdOptions) {
	testDir := t.TempDir()
	opts := testutils.RunDnoteCmdOptions{
		Env: []string{
			fmt.Sprintf("XDG_CONFIG_HOME=%s", testDir),
			fmt.Sprintf("XDG_DATA_HOME=%s", testDir),
			fmt.Sprintf("XDG_CACHE_HOME=%s", testDir),
		},
	}
	return testDir, opts
}

func TestMain(m *testing.M) {
	if err := exec.Command("go", "build", "--tags", "fts5", "-o", binaryName).Run(); err != nil {
		log.Print(errors.Wrap(err, "building a binary").Error())
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func TestInit(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	// run an arbitrary command to trigger initialization
	testutils.RunDnoteCmd(t, opts, binaryName, "list", "--roots")

	db := database.OpenTestDB(t, testDir)

	ok, err := utils.FileExists(fmt.Sprintf("%s/%s/%s", testDir, consts.LflowDirName, consts.ConfigFilename))
	if err != nil {
		t.Fatal(errors.Wrap(err, "checking if lflow config exists"))
	}
	if !ok {
		t.Errorf("config file was not initialized")
	}

	// the node model should exist; legacy tables should be gone (converted)
	var nodesTableCount, wfMirrorsTableCount, booksTableCount, systemTableCount int
	database.MustScan(t, "counting nodes table",
		db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", "table", "nodes"), &nodesTableCount)
	database.MustScan(t, "counting wf_mirrors table",
		db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", "table", "wf_mirrors"), &wfMirrorsTableCount)
	database.MustScan(t, "counting books table",
		db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", "table", "books"), &booksTableCount)
	database.MustScan(t, "counting system table",
		db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", "table", "system"), &systemTableCount)

	assert.Equal(t, nodesTableCount, 1, "nodes table count mismatch")
	assert.Equal(t, wfMirrorsTableCount, 1, "wf_mirrors table count mismatch")
	assert.Equal(t, booksTableCount, 0, "books table should have been dropped")
	assert.Equal(t, systemTableCount, 1, "system table count mismatch")

	var lastUpgrade, lastMaxUSN, lastSyncAt string
	database.MustScan(t, "scanning last upgrade",
		db.QueryRow("SELECT value FROM system WHERE key = ?", consts.SystemLastUpgrade), &lastUpgrade)
	database.MustScan(t, "scanning last max usn",
		db.QueryRow("SELECT value FROM system WHERE key = ?", consts.SystemLastMaxUSN), &lastMaxUSN)
	database.MustScan(t, "scanning last sync at",
		db.QueryRow("SELECT value FROM system WHERE key = ?", consts.SystemLastSyncAt), &lastSyncAt)

	assert.NotEqual(t, lastUpgrade, "", "last upgrade should not be empty")
	assert.NotEqual(t, lastMaxUSN, "", "last max usn should not be empty")
	assert.NotEqual(t, lastSyncAt, "", "last sync at should not be empty")
}

func TestAddRootAndChild(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "add", "--root", "experiment results")
	testutils.RunDnoteCmd(t, opts, binaryName, "add", "experiment results", "baseline numbers")
	testutils.RunDnoteCmd(t, opts, binaryName, "add", "experiment results", "attempt 2")

	db := database.OpenTestDB(t, testDir)

	var rootUUID string
	database.MustScan(t, "getting root uuid",
		db.QueryRow("SELECT uuid FROM nodes WHERE name = ? AND parent_uuid = ''", "experiment results"), &rootUUID)

	var childCount int
	database.MustScan(t, "counting children",
		db.QueryRow("SELECT count(*) FROM nodes WHERE parent_uuid = ?", rootUUID), &childCount)
	assert.Equal(t, childCount, 2, "child count mismatch")

	// rank ordering
	var firstChild string
	database.MustScan(t, "getting first child",
		db.QueryRow("SELECT name FROM nodes WHERE parent_uuid = ? ORDER BY rank LIMIT 1", rootUUID), &firstChild)
	assert.Equal(t, firstChild, "baseline numbers", "first child mismatch")

	// everything new must be dirty for sync
	var dirtyCount int
	database.MustScan(t, "counting dirty",
		db.QueryRow("SELECT count(*) FROM nodes WHERE dirty"), &dirtyCount)
	assert.Equal(t, dirtyCount, 3, "dirty count mismatch")
}

func TestAppendStdin(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "add", "--root", "bench log")

	writeLines := func(stdout io.Reader, stdin io.WriteCloser) error {
		if _, err := io.WriteString(stdin, "line one\nline two\nline three\n"); err != nil {
			return errors.Wrap(err, "writing stdin")
		}
		stdin.Close()
		return nil
	}
	testutils.MustWaitDnoteCmd(t, opts, writeLines, binaryName, "append", "bench log")

	db := database.OpenTestDB(t, testDir)

	var rootUUID string
	database.MustScan(t, "getting root uuid",
		db.QueryRow("SELECT uuid FROM nodes WHERE name = ?", "bench log"), &rootUUID)

	var childCount int
	database.MustScan(t, "counting children",
		db.QueryRow("SELECT count(*) FROM nodes WHERE parent_uuid = ?", rootUUID), &childCount)
	assert.Equal(t, childCount, 3, "each stdin line should become a child node")
}

func TestAppendNoteFlag(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "add", "--root", "target")
	testutils.RunDnoteCmd(t, opts, binaryName, "append", "target", "some context", "--note")

	db := database.OpenTestDB(t, testDir)

	var note string
	database.MustScan(t, "getting note",
		db.QueryRow("SELECT note FROM nodes WHERE name = ?", "target"), &note)
	assert.Equal(t, note, "some context", "note content mismatch")
}

func TestList(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	// initialize, then seed
	testutils.RunDnoteCmd(t, opts, binaryName, "list", "--roots")
	db := database.OpenTestDB(t, testDir)
	testutils.SetupNodes1(t, db)
	db.Close()

	out := testutils.RunDnoteCmd(t, opts, binaryName, "list", "experiment results")
	if !strings.Contains(out, "- baseline numbers") {
		t.Errorf("markdown output missing child: %q", out)
	}
	if !strings.Contains(out, "  - parse: 1.42s") {
		t.Errorf("markdown output missing indented grandchild: %q", out)
	}

	out = testutils.RunDnoteCmd(t, opts, binaryName, "list", "experiment results", "--format", "json")
	var tree struct {
		Name     string `json:"name"`
		Children []struct {
			Name string `json:"name"`
		} `json:"children"`
	}
	if err := json.Unmarshal([]byte(out), &tree); err != nil {
		t.Fatalf("invalid json output: %v: %q", err, out)
	}
	assert.Equal(t, tree.Name, "experiment results", "json root name mismatch")
	assert.Equal(t, len(tree.Children), 2, "json child count mismatch")

	out = testutils.RunDnoteCmd(t, opts, binaryName, "list", "--roots")
	if !strings.Contains(out, "experiment results") || !strings.Contains(out, "reading list") {
		t.Errorf("roots listing missing roots: %q", out)
	}
}

func TestFindPrintAndID(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "list", "--roots")
	db := database.OpenTestDB(t, testDir)
	testutils.SetupNodes1(t, db)
	db.Close()

	out := testutils.RunDnoteCmd(t, opts, binaryName, "find", "experiment", "--print")
	if !strings.Contains(out, "- baseline numbers") {
		t.Errorf("find --print missing outline: %q", out)
	}

	out = testutils.RunDnoteCmd(t, opts, binaryName, "find", "experiment results", "--id")
	assert.Equal(t, strings.TrimSpace(out), "root-1-uuid", "find --id mismatch")
}

func TestFindMissExitsNonZero(t *testing.T) {
	_, opts := setupTestEnv(t)

	cmd, _, _, err := testutils.NewDnoteCmd(opts, binaryName, "find", "quantum", "--print")
	if err != nil {
		t.Fatal(err)
	}
	err = cmd.Run()
	if err == nil {
		t.Fatal("find on a miss should exit non-zero")
	}
}

func TestRemove(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "list", "--roots")
	db := database.OpenTestDB(t, testDir)
	testutils.SetupNodes1(t, db)
	db.Close()

	testutils.RunDnoteCmd(t, opts, binaryName, "rm", "-f", "baseline numbers")

	db = database.OpenTestDB(t, testDir)
	defer db.Close()

	// the node and its child are tombstoned, not expunged (so the delete syncs)
	var deletedCount int
	database.MustScan(t, "counting deleted",
		db.QueryRow("SELECT count(*) FROM nodes WHERE deleted = 1 AND dirty = 1"), &deletedCount)
	assert.Equal(t, deletedCount, 2, "subtree should be tombstoned")
}

func TestMove(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "list", "--roots")
	db := database.OpenTestDB(t, testDir)
	testutils.SetupNodes1(t, db)
	db.Close()

	testutils.RunDnoteCmd(t, opts, binaryName, "mv", "attempt 2", "reading list")

	db = database.OpenTestDB(t, testDir)
	defer db.Close()

	var parentUUID string
	database.MustScan(t, "getting parent",
		db.QueryRow("SELECT parent_uuid FROM nodes WHERE uuid = ?", "child-2-uuid"), &parentUUID)
	assert.Equal(t, parentUUID, "root-2-uuid", "node was not moved")
}

func TestComplete(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "list", "--roots")
	db := database.OpenTestDB(t, testDir)
	testutils.SetupNodes1(t, db)
	db.Close()

	testutils.RunDnoteCmd(t, opts, binaryName, "complete", "attempt 2")

	db = database.OpenTestDB(t, testDir)

	var completedAt int64
	database.MustScan(t, "getting completed_at",
		db.QueryRow("SELECT completed_at FROM nodes WHERE uuid = ?", "child-2-uuid"), &completedAt)
	if completedAt == 0 {
		t.Error("node should be completed")
	}
	db.Close()

	testutils.RunDnoteCmd(t, opts, binaryName, "uncomplete", "attempt 2")

	db = database.OpenTestDB(t, testDir)
	defer db.Close()
	database.MustScan(t, "getting completed_at",
		db.QueryRow("SELECT completed_at FROM nodes WHERE uuid = ?", "child-2-uuid"), &completedAt)
	assert.Equal(t, completedAt, int64(0), "node should be uncompleted")
}

func TestExport(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "list", "--roots")
	db := database.OpenTestDB(t, testDir)
	testutils.SetupNodes1(t, db)
	db.Close()

	out := testutils.RunDnoteCmd(t, opts, binaryName, "export")
	var forest []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &forest); err != nil {
		t.Fatalf("invalid export json: %v", err)
	}
	assert.Equal(t, len(forest), 2, "export should contain both roots")
}

func TestDBPathFlag(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	customDBPath := fmt.Sprintf("%s/custom.db", testDir)
	testutils.RunDnoteCmd(t, opts, binaryName, "list", "--roots", "--dbPath", customDBPath)

	ok, err := utils.FileExists(customDBPath)
	if err != nil {
		t.Fatal(errors.Wrap(err, "checking if custom db exists"))
	}
	if !ok {
		t.Errorf("custom db file was not created")
	}
}
