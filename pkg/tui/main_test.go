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

	"github.com/lflow/lflow/pkg/shared/assert"
	"github.com/lflow/lflow/pkg/tui/consts"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/testutils"
	"github.com/lflow/lflow/pkg/tui/utils"
	"github.com/pkg/errors"
)

var binaryName = "test-lflow"

// setupTestEnv creates a unique test directory for parallel test execution
func setupTestEnv(t *testing.T) (string, testutils.RunDnoteCmdOptions) {
	testDir := t.TempDir()
	opts := testutils.RunDnoteCmdOptions{
		Env: []string{
			// HOME isolates ~/.lflow/settings.json to the test dir
			fmt.Sprintf("HOME=%s", testDir),
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
	testutils.RunDnoteCmd(t, opts, binaryName, "node", "list")

	db := database.OpenTestDB(t, testDir)

	ok, err := utils.FileExists(fmt.Sprintf("%s/%s/%s", testDir, consts.LflowHomeDirName, consts.SettingsFilename))
	if err != nil {
		t.Fatal(errors.Wrap(err, "checking if lflow settings exist"))
	}
	if !ok {
		t.Errorf("settings file was not initialized")
	}

	// the node model should exist; legacy tables should be gone (converted)
	var nodesTableCount, booksTableCount, systemTableCount int
	database.MustScan(t, "counting nodes table",
		db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", "table", "nodes"), &nodesTableCount)
	database.MustScan(t, "counting books table",
		db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", "table", "books"), &booksTableCount)
	database.MustScan(t, "counting system table",
		db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", "table", "system"), &systemTableCount)

	assert.Equal(t, nodesTableCount, 1, "nodes table count mismatch")
	assert.Equal(t, booksTableCount, 0, "books table should have been dropped")
	assert.Equal(t, systemTableCount, 1, "system table count mismatch")

	var lastUpgrade string
	database.MustScan(t, "scanning last upgrade",
		db.QueryRow("SELECT value FROM system WHERE key = ?", consts.SystemLastUpgrade), &lastUpgrade)

	assert.NotEqual(t, lastUpgrade, "", "last upgrade should not be empty")
}

func TestAddRootAndChild(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "add", "experiment results")
	testutils.RunDnoteCmd(t, opts, binaryName, "node", "add", "--parent", "experiment results", "baseline numbers")
	testutils.RunDnoteCmd(t, opts, binaryName, "node", "add", "--parent", "experiment results", "attempt 2")

	db := database.OpenTestDB(t, testDir)

	var rootUUID string
	database.MustScan(t, "getting root uuid",
		db.QueryRow("SELECT uuid FROM nodes WHERE name = ?", "experiment results"), &rootUUID)

	var childCount int
	database.MustScan(t, "counting children",
		db.QueryRow("SELECT count(*) FROM nodes WHERE parent_uuid = ?", rootUUID), &childCount)
	assert.Equal(t, childCount, 2, "child count mismatch")

	// rank ordering
	var firstChild string
	database.MustScan(t, "getting first child",
		db.QueryRow("SELECT name FROM nodes WHERE parent_uuid = ? ORDER BY rank LIMIT 1", rootUUID), &firstChild)
	assert.Equal(t, firstChild, "baseline numbers", "first child mismatch")

	// all three nodes were added under root
	var addedCount int
	database.MustScan(t, "counting added",
		db.QueryRow("SELECT count(*) FROM nodes WHERE uuid NOT IN (?, ?)", database.RootUUID, database.TempUUID), &addedCount)
	assert.Equal(t, addedCount, 3, "added count mismatch")
}

func TestAppendStdin(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "add", "bench log")

	writeLines := func(stdout io.Reader, stdin io.WriteCloser) error {
		if _, err := io.WriteString(stdin, "line one\nline two\nline three\n"); err != nil {
			return errors.Wrap(err, "writing stdin")
		}
		stdin.Close()
		return nil
	}
	testutils.MustWaitDnoteCmd(t, opts, writeLines, binaryName, "node", "add", "--parent", "bench log")

	db := database.OpenTestDB(t, testDir)

	var rootUUID string
	database.MustScan(t, "getting root uuid",
		db.QueryRow("SELECT uuid FROM nodes WHERE name = ?", "bench log"), &rootUUID)

	var childCount int
	database.MustScan(t, "counting children",
		db.QueryRow("SELECT count(*) FROM nodes WHERE parent_uuid = ?", rootUUID), &childCount)
	assert.Equal(t, childCount, 3, "each stdin line should become a child node")
}

func TestAddNoteFlag(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "add", "target")
	testutils.RunDnoteCmd(t, opts, binaryName, "node", "add", "--parent", "target", "child item", "--note", "some context")

	db := database.OpenTestDB(t, testDir)

	// --note sets the note on the added node, leaving the parent untouched
	var parentNote string
	database.MustScan(t, "getting parent note",
		db.QueryRow("SELECT note FROM nodes WHERE name = ?", "target"), &parentNote)
	assert.Equal(t, parentNote, "", "parent note should be untouched")

	var childNote string
	database.MustScan(t, "getting child note",
		db.QueryRow("SELECT note FROM nodes WHERE name = ?", "child item"), &childNote)
	assert.Equal(t, childNote, "some context", "note should land on the added node")
}

func TestAddChipifiesText(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "add", "ship #project by 2026-07-01 see [docs](https://x.com)")

	db := database.OpenTestDB(t, testDir)

	// three chips recorded: the tag, the date and the link
	var chipCount int
	database.MustScan(t, "counting chips", db.QueryRow("SELECT count(*) FROM chips"), &chipCount)
	assert.Equal(t, chipCount, 3, "tag, date and link should each become a chip")

	// the stored name carries anchors, not the literal inline forms
	var name string
	database.MustScan(t, "getting name",
		db.QueryRow("SELECT name FROM nodes WHERE name LIKE 'ship%'"), &name)
	if strings.Contains(name, "#project") || strings.Contains(name, "[docs]") {
		t.Errorf("inline forms should be replaced by anchors, got %q", name)
	}

	// list resolves the anchors back to their display forms
	out := testutils.RunDnoteCmd(t, opts, binaryName, "node", "list")
	if !strings.Contains(out, "#project") {
		t.Errorf("list should resolve the tag chip, got %q", out)
	}
}

func TestAddRawSkipsChipify(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "add", "--raw", "literal #notatag text")

	db := database.OpenTestDB(t, testDir)

	var chipCount int
	database.MustScan(t, "counting chips", db.QueryRow("SELECT count(*) FROM chips"), &chipCount)
	assert.Equal(t, chipCount, 0, "--raw should create no chips")

	var name string
	database.MustScan(t, "getting name",
		db.QueryRow("SELECT name FROM nodes WHERE name LIKE 'literal%'"), &name)
	assert.Equal(t, name, "literal #notatag text", "--raw should store text verbatim")
}

func TestAddStyleFlags(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "add", "styled item", "--bold", "--color", "blue")

	db := database.OpenTestDB(t, testDir)

	var style string
	database.MustScan(t, "getting style",
		db.QueryRow("SELECT style FROM nodes WHERE name = ?", "styled item"), &style)
	assert.Equal(t, style, "bold,color:blue", "style tokens mismatch")
}

func TestEditStyleAndType(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "add", "edit me")
	testutils.RunDnoteCmd(t, opts, binaryName, "node", "edit", "edit me", "--type", "h2", "--underline", "--color", "red")

	db := database.OpenTestDB(t, testDir)

	var typ, style string
	database.MustScan(t, "getting type and style",
		db.QueryRow("SELECT type, style FROM nodes WHERE name = ?", "edit me"), &typ, &style)
	assert.Equal(t, typ, "h2", "type mismatch")
	assert.Equal(t, style, "underline,color:red", "style tokens mismatch")

	// editing again preserves untouched style aspects and unsets bold/color via flags
	testutils.RunDnoteCmd(t, opts, binaryName, "node", "edit", "edit me", "--color", "")
	database.MustScan(t, "getting style after color clear",
		db.QueryRow("SELECT style FROM nodes WHERE name = ?", "edit me"), &style)
	assert.Equal(t, style, "underline", "clearing color should preserve other tokens")
}

func TestList(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	// initialize, then seed
	testutils.RunDnoteCmd(t, opts, binaryName, "node", "list")
	db := database.OpenTestDB(t, testDir)
	testutils.SetupNodes1(t, db)
	db.Close()

	out := testutils.RunDnoteCmd(t, opts, binaryName, "node", "list", "experiment results")
	if !strings.Contains(out, "- baseline numbers") {
		t.Errorf("markdown output missing child: %q", out)
	}
	if !strings.Contains(out, "  - parse: 1.42s") {
		t.Errorf("markdown output missing indented grandchild: %q", out)
	}

	out = testutils.RunDnoteCmd(t, opts, binaryName, "node", "list", "experiment results", "--format", "json")
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

	out = testutils.RunDnoteCmd(t, opts, binaryName, "node", "list")
	if !strings.Contains(out, "experiment results") || !strings.Contains(out, "reading list") {
		t.Errorf("roots listing missing roots: %q", out)
	}
}

func TestListResolvesQuery(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "list")
	db := database.OpenTestDB(t, testDir)
	testutils.SetupNodes1(t, db)
	db.Close()

	out := testutils.RunDnoteCmd(t, opts, binaryName, "node", "list", "experiment")
	if !strings.Contains(out, "- baseline numbers") {
		t.Errorf("list by query missing outline: %q", out)
	}
}

func TestResolveMissExitsNonZero(t *testing.T) {
	_, opts := setupTestEnv(t)

	cmd, _, _, err := testutils.NewDnoteCmd(opts, binaryName, "node", "list", "quantum")
	if err != nil {
		t.Fatal(err)
	}
	err = cmd.Run()
	if err == nil {
		t.Fatal("resolving a miss should exit non-zero")
	}
}

func TestRemove(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "list")
	db := database.OpenTestDB(t, testDir)
	testutils.SetupNodes1(t, db)
	db.Close()

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "remove", "-f", "baseline numbers")

	db = database.OpenTestDB(t, testDir)
	defer db.Close()

	// the node and its child are tombstoned, not expunged
	var deletedCount int
	database.MustScan(t, "counting deleted",
		db.QueryRow("SELECT count(*) FROM nodes WHERE deleted = 1"), &deletedCount)
	assert.Equal(t, deletedCount, 2, "subtree should be tombstoned")
}

func TestMove(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "list")
	db := database.OpenTestDB(t, testDir)
	testutils.SetupNodes1(t, db)
	db.Close()

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "move", "attempt 2", "reading list")

	db = database.OpenTestDB(t, testDir)
	defer db.Close()

	var parentUUID string
	database.MustScan(t, "getting parent",
		db.QueryRow("SELECT parent_uuid FROM nodes WHERE uuid = ?", "child-2-uuid"), &parentUUID)
	assert.Equal(t, parentUUID, "root-2-uuid", "node was not moved")
}

func TestComplete(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "list")
	db := database.OpenTestDB(t, testDir)
	testutils.SetupNodes1(t, db)
	db.Close()

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "edit", "attempt 2", "--state", "complete")

	db = database.OpenTestDB(t, testDir)

	var completedAt int64
	database.MustScan(t, "getting completed_at",
		db.QueryRow("SELECT completed_at FROM nodes WHERE uuid = ?", "child-2-uuid"), &completedAt)
	if completedAt == 0 {
		t.Error("node should be completed")
	}
	db.Close()

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "edit", "attempt 2", "--state", "uncomplete")

	db = database.OpenTestDB(t, testDir)
	defer db.Close()
	database.MustScan(t, "getting completed_at",
		db.QueryRow("SELECT completed_at FROM nodes WHERE uuid = ?", "child-2-uuid"), &completedAt)
	assert.Equal(t, completedAt, int64(0), "node should be uncompleted")
}

func TestExport(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "list")
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

func TestDBPathConfig(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	// dbPath is configured in the settings file, never by flag
	customDBPath := fmt.Sprintf("%s/custom.db", testDir)
	configDir := fmt.Sprintf("%s/%s", testDir, consts.LflowHomeDirName)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(errors.Wrap(err, "creating config dir"))
	}
	configBody := fmt.Sprintf("{\n  \"editor\": \"vi\",\n  \"dbPath\": %q\n}\n", customDBPath)
	if err := os.WriteFile(fmt.Sprintf("%s/%s", configDir, consts.SettingsFilename), []byte(configBody), 0644); err != nil {
		t.Fatal(errors.Wrap(err, "writing settings"))
	}

	testutils.RunDnoteCmd(t, opts, binaryName, "node", "list")

	ok, err := utils.FileExists(customDBPath)
	if err != nil {
		t.Fatal(errors.Wrap(err, "checking if custom db exists"))
	}
	if !ok {
		t.Errorf("custom db file was not created")
	}
}
