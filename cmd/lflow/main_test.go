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

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/utils"
	"github.com/lflow/lflow/packages/utils/assert"
	"github.com/lflow/lflow/packages/utils/consts"
	"github.com/lflow/lflow/tests/cmdhelper"
	"github.com/pkg/errors"
)

var binaryName = "test-lflow"

// setupTestEnv creates a unique test directory for parallel test execution
func setupTestEnv(t *testing.T) (string, cmdhelper.RunLflowCmdOptions) {
	testDir := t.TempDir()
	opts := cmdhelper.RunLflowCmdOptions{
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
	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list")

	db := database.OpenTestDB(t, testDir)

	ok, err := utils.FileExists(fmt.Sprintf("%s/%s/%s", testDir, consts.LflowHomeDirName, consts.SettingsFilename))
	if err != nil {
		t.Fatal(errors.Wrap(err, "checking if lflow settings exist"))
	}
	if !ok {
		t.Errorf("settings file was not initialized")
	}

	// the node model should exist; legacy tables never do (lflow has no migrations)
	var nodesTableCount, booksTableCount, systemTableCount int
	database.MustScan(t, "counting nodes table",
		db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", "table", "nodes"), &nodesTableCount)
	database.MustScan(t, "counting books table",
		db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", "table", "books"), &booksTableCount)
	database.MustScan(t, "counting system table",
		db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", "table", "system"), &systemTableCount)

	assert.Equal(t, nodesTableCount, 1, "nodes table count mismatch")
	assert.Equal(t, booksTableCount, 0, "books table should not exist")
	assert.Equal(t, systemTableCount, 1, "system table count mismatch")

	var lastUpgrade string
	database.MustScan(t, "scanning last upgrade",
		db.QueryRow("SELECT value FROM system WHERE key = ?", consts.SystemLastUpgrade), &lastUpgrade)

	assert.NotEqual(t, lastUpgrade, "", "last upgrade should not be empty")
}

func TestAddRootAndChild(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "experiment results")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "--parent", "experiment results", "baseline numbers")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "--parent", "experiment results", "attempt 2")

	db := database.OpenTestDB(t, testDir)

	var rootUUID string
	database.MustScan(t, "getting root uuid",
		db.QueryRow("SELECT uuid FROM nodes WHERE name = ?", "experiment results"), &rootUUID)

	var childCount int
	database.MustScan(t, "counting children",
		db.QueryRow("SELECT count(*) FROM nodes WHERE parent_uuid = ?", rootUUID), &childCount)
	assert.Equal(t, childCount, 2, "child count mismatch")

	// rank ordering: a freshly added parent is priority up, so the newest
	// child lands on top
	var firstChild string
	database.MustScan(t, "getting first child",
		db.QueryRow("SELECT name FROM nodes WHERE parent_uuid = ? ORDER BY rank LIMIT 1", rootUUID), &firstChild)
	assert.Equal(t, firstChild, "attempt 2", "first child mismatch")

	// all three nodes were added under root
	var addedCount int
	database.MustScan(t, "counting added",
		db.QueryRow("SELECT count(*) FROM nodes WHERE uuid NOT IN (?, ?)", database.RootUUID, database.TempUUID), &addedCount)
	assert.Equal(t, addedCount, 3, "added count mismatch")
}

func TestAppendStdin(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "bench log")

	writeLines := func(stdout io.Reader, stdin io.WriteCloser) error {
		if _, err := io.WriteString(stdin, "line one\nline two\nline three\n"); err != nil {
			return errors.Wrap(err, "writing stdin")
		}
		stdin.Close()
		return nil
	}
	cmdhelper.MustWaitLflowCmd(t, opts, writeLines, binaryName, "node", "add", "--parent", "bench log")

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

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "target")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "--parent", "target", "child item", "--note", "some context")

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

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "ship #project by 2026-07-01 see [docs](https://x.com)")

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
	out := cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list")
	if !strings.Contains(out, "#project") {
		t.Errorf("list should resolve the tag chip, got %q", out)
	}
}

func TestAddRawSkipsChipify(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "--raw", "literal #notatag text")

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

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "styled item", "--bold", "--color", "blue")

	db := database.OpenTestDB(t, testDir)

	var style string
	database.MustScan(t, "getting style",
		db.QueryRow("SELECT style FROM nodes WHERE name = ?", "styled item"), &style)
	assert.Equal(t, style, "bold,color:blue", "style tokens mismatch")
}

func TestEditStyleAndType(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "edit me")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "edit", "edit me", "--type", "h2", "--underline", "--color", "red")

	db := database.OpenTestDB(t, testDir)

	var typ, style string
	database.MustScan(t, "getting type and style",
		db.QueryRow("SELECT type, style FROM nodes WHERE name = ?", "edit me"), &typ, &style)
	assert.Equal(t, typ, "h2", "type mismatch")
	assert.Equal(t, style, "underline,color:red", "style tokens mismatch")

	// editing again preserves untouched style aspects and unsets bold/color via flags
	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "edit", "edit me", "--color", "")
	database.MustScan(t, "getting style after color clear",
		db.QueryRow("SELECT style FROM nodes WHERE name = ?", "edit me"), &style)
	assert.Equal(t, style, "underline", "clearing color should preserve other tokens")
}

func TestList(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	// initialize, then seed
	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list")
	db := database.OpenTestDB(t, testDir)
	cmdhelper.SetupNodes1(t, db)
	db.Close()

	out := cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list", "experiment results")
	if !strings.Contains(out, "- baseline numbers") {
		t.Errorf("markdown output missing child: %q", out)
	}
	if !strings.Contains(out, "  - parse: 1.42s") {
		t.Errorf("markdown output missing indented grandchild: %q", out)
	}

	out = cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list", "experiment results", "--format", "json")
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

	out = cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list")
	if !strings.Contains(out, "experiment results") || !strings.Contains(out, "reading list") {
		t.Errorf("roots listing missing roots: %q", out)
	}
}

func TestListResolvesQuery(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list")
	db := database.OpenTestDB(t, testDir)
	cmdhelper.SetupNodes1(t, db)
	db.Close()

	out := cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list", "experiment")
	if !strings.Contains(out, "- baseline numbers") {
		t.Errorf("list by query missing outline: %q", out)
	}
}

func TestResolveMissExitsNonZero(t *testing.T) {
	_, opts := setupTestEnv(t)

	cmd, _, _, err := cmdhelper.NewLflowCmd(opts, binaryName, "node", "list", "quantum")
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

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list")
	db := database.OpenTestDB(t, testDir)
	cmdhelper.SetupNodes1(t, db)
	db.Close()

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "remove", "-f", "baseline numbers")

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

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list")
	db := database.OpenTestDB(t, testDir)
	cmdhelper.SetupNodes1(t, db)
	db.Close()

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "move", "attempt 2", "reading list")

	db = database.OpenTestDB(t, testDir)
	defer db.Close()

	var parentUUID string
	database.MustScan(t, "getting parent",
		db.QueryRow("SELECT parent_uuid FROM nodes WHERE uuid = ?", "child-2-uuid"), &parentUUID)
	assert.Equal(t, parentUUID, "root-2-uuid", "node was not moved")
}

func TestComplete(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list")
	db := database.OpenTestDB(t, testDir)
	cmdhelper.SetupNodes1(t, db)
	db.Close()

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "edit", "attempt 2", "--state", "complete")

	db = database.OpenTestDB(t, testDir)

	var completedAt int64
	database.MustScan(t, "getting completed_at",
		db.QueryRow("SELECT completed_at FROM nodes WHERE uuid = ?", "child-2-uuid"), &completedAt)
	if completedAt == 0 {
		t.Error("node should be completed")
	}
	db.Close()

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "edit", "attempt 2", "--state", "uncomplete")

	db = database.OpenTestDB(t, testDir)
	defer db.Close()
	database.MustScan(t, "getting completed_at",
		db.QueryRow("SELECT completed_at FROM nodes WHERE uuid = ?", "child-2-uuid"), &completedAt)
	assert.Equal(t, completedAt, int64(0), "node should be uncompleted")
}

func TestExport(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list")
	db := database.OpenTestDB(t, testDir)
	cmdhelper.SetupNodes1(t, db)
	db.Close()

	out := cmdhelper.RunLflowCmd(t, opts, binaryName, "export")
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

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "list")

	ok, err := utils.FileExists(customDBPath)
	if err != nil {
		t.Fatal(errors.Wrap(err, "checking if custom db exists"))
	}
	if !ok {
		t.Errorf("custom db file was not created")
	}
}

// TestSuggestAddNeedsApproval walks the whole review loop for a proposed node:
// suggesting changes nothing, approving creates the node.
func TestSuggestAddNeedsApproval(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "reading list")
	out := cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "add",
		"--parent", "reading list", "--message", "found this", "Designing Data-Intensive Applications")
	if !strings.Contains(out, "suggested 1 node") {
		t.Fatalf("suggest add output = %q", out)
	}

	db := database.OpenTestDB(t, testDir)

	// the proposal exists, the outline does not have it yet
	var pending int
	database.MustScan(t, "counting pending",
		db.QueryRow("SELECT count(*) FROM suggestions WHERE status = 'pending'"), &pending)
	assert.Equal(t, pending, 1, "suggestion should be stored pending")

	var nodes int
	database.MustScan(t, "counting nodes",
		db.QueryRow("SELECT count(*) FROM nodes WHERE name = ?", "Designing Data-Intensive Applications"), &nodes)
	assert.Equal(t, nodes, 0, "a pending suggestion must not touch the outline")

	var id string
	database.MustScan(t, "getting suggestion id",
		db.QueryRow("SELECT uuid FROM suggestions LIMIT 1"), &id)
	db.Close()

	listed := cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "list")
	if !strings.Contains(listed, id[:6]) {
		t.Fatalf("suggest list did not show %s: %q", id[:6], listed)
	}

	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "approve", id[:6])

	db = database.OpenTestDB(t, testDir)
	defer db.Close()

	var parentUUID, childName, status, resultUUID string
	database.MustScan(t, "getting parent",
		db.QueryRow("SELECT uuid FROM nodes WHERE name = ?", "reading list"), &parentUUID)
	database.MustScan(t, "getting created child",
		db.QueryRow("SELECT name FROM nodes WHERE parent_uuid = ?", parentUUID), &childName)
	assert.Equal(t, childName, "Designing Data-Intensive Applications", "approval should create the node")

	database.MustScan(t, "getting settled suggestion",
		db.QueryRow("SELECT status, result_uuid FROM suggestions WHERE uuid = ?", id), &status, &resultUUID)
	assert.Equal(t, status, database.SuggestApproved, "suggestion should be approved")
	if resultUUID == "" {
		t.Fatal("approved add did not record the node it created")
	}
}

// TestSuggestEditRejectKeepsText: a rejected text edit never reaches the node.
func TestSuggestEditRejectKeepsText(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "ship the thing")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "edit", "ship the thing",
		"--name", "ship the other thing")

	db := database.OpenTestDB(t, testDir)
	var id string
	database.MustScan(t, "getting suggestion id",
		db.QueryRow("SELECT uuid FROM suggestions LIMIT 1"), &id)
	db.Close()

	shown := cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "show", id[:6])
	if !strings.Contains(shown, "ship the other thing") {
		t.Fatalf("suggest show did not render the proposal: %q", shown)
	}

	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "reject", id[:6])

	db = database.OpenTestDB(t, testDir)
	defer db.Close()

	var name, status string
	database.MustScan(t, "getting node name",
		db.QueryRow("SELECT name FROM nodes WHERE uuid IN (SELECT target_uuid FROM suggestions WHERE uuid = ?)", id), &name)
	assert.Equal(t, name, "ship the thing", "a rejected suggestion must not change the node")

	database.MustScan(t, "getting status",
		db.QueryRow("SELECT status FROM suggestions WHERE uuid = ?", id), &status)
	assert.Equal(t, status, database.SuggestRejected, "suggestion should be rejected")
}

// TestSuggestApproveEditAppliesText: approving a text edit rewrites the node.
func TestSuggestApproveEditAppliesText(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "draft heading")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "edit", "draft heading",
		"--name", "final heading", "--type", "h1")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "approve", "--all")

	db := database.OpenTestDB(t, testDir)
	defer db.Close()

	var name, typ string
	database.MustScan(t, "getting edited node",
		db.QueryRow("SELECT name, type FROM nodes WHERE name = ?", "final heading"), &name, &typ)
	assert.Equal(t, typ, database.TypeH1, "approval should apply the proposed type")
}

// TestSuggestApproveSkipsDriftedTarget: a proposal made against text that has
// since changed is held back until --force.
func TestSuggestApproveSkipsDriftedTarget(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "original text")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "edit", "original text", "--name", "suggested text")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "edit", "original text", "--name", "moved on")

	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "approve", "--all")

	db := database.OpenTestDB(t, testDir)
	var name, status string
	database.MustScan(t, "getting node name",
		db.QueryRow("SELECT name FROM nodes WHERE name = ?", "moved on"), &name)
	database.MustScan(t, "getting status",
		db.QueryRow("SELECT status FROM suggestions LIMIT 1"), &status)
	assert.Equal(t, status, database.SuggestPending, "a drifted suggestion should stay pending")
	db.Close()

	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "approve", "--all", "--force")

	db = database.OpenTestDB(t, testDir)
	defer db.Close()
	database.MustScan(t, "getting forced status",
		db.QueryRow("SELECT status FROM suggestions LIMIT 1"), &status)
	assert.Equal(t, status, database.SuggestApproved, "--force should apply the drifted suggestion")

	var forced string
	database.MustScan(t, "getting forced name",
		db.QueryRow("SELECT name FROM nodes WHERE name = ?", "suggested text"), &forced)
	assert.Equal(t, forced, "suggested text", "--force should rewrite the node")
}

// TestRemoveSettlesSuggestionsAboutTheNode: deleting a node settles the
// proposals about it there and then, so no zombie ever reaches the queue. This
// is the fix for the ghost that kept coming back — the boot sweep and the
// batch-approve skip below are backstops for ghosts already on disk, not the
// first line of defence.
func TestRemoveSettlesSuggestionsAboutTheNode(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "keeper")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "doomed")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "complete", "doomed")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "complete", "keeper")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "remove", "doomed", "--force")

	db := database.OpenTestDB(t, testDir)
	var status string
	database.MustScan(t, "getting settled status",
		db.QueryRow("SELECT status FROM suggestions WHERE target_uuid IN (SELECT uuid FROM nodes WHERE name = 'doomed')"), &status)
	assert.Equal(t, status, database.SuggestRejected, "removing a node should settle its suggestions")
	db.Close()

	// the queue is now clean, so a batch approval has nothing to warn about
	out := cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "approve", "--all")
	if strings.Contains(out, "target was deleted") {
		t.Fatalf("a settled suggestion still reached the queue: %q", out)
	}

	db = database.OpenTestDB(t, testDir)
	defer db.Close()
	database.MustScan(t, "getting keeper status",
		db.QueryRow("SELECT status FROM suggestions WHERE target_uuid IN (SELECT uuid FROM nodes WHERE name = 'keeper')"), &status)
	assert.Equal(t, status, database.SuggestApproved, "the live suggestion should approve")
}

// TestSuggestApproveSkipsZombieTargets: a proposal whose node was deleted can
// never be applied — a batch approval skips it with a warning instead of
// aborting the whole queue. The delete path settles its own suggestions now, so
// the zombie here is tombstoned behind lflow's back: a ghost left on disk by an
// older version, or by another writer touching the DB directly.
func TestSuggestApproveSkipsZombieTargets(t *testing.T) {
	testDir, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "keeper")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "doomed")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "complete", "doomed")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "complete", "keeper")

	db := database.OpenTestDB(t, testDir)
	var doomed string
	database.MustScan(t, "getting doomed uuid",
		db.QueryRow("SELECT uuid FROM nodes WHERE name = ?", "doomed"), &doomed)
	if err := (database.Node{UUID: doomed}).Expunge(db); err != nil {
		t.Fatal(err)
	}
	db.Close()

	out := cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "approve", "--all")
	if !strings.Contains(out, "target was deleted") {
		t.Fatalf("approve --all did not flag the zombie: %q", out)
	}

	db = database.OpenTestDB(t, testDir)
	defer db.Close()

	var status string
	database.MustScan(t, "getting zombie status",
		db.QueryRow("SELECT status FROM suggestions WHERE target_uuid = ?", doomed), &status)
	assert.Equal(t, status, database.SuggestPending, "a zombie should stay pending, not approve")

	database.MustScan(t, "getting keeper status",
		db.QueryRow("SELECT status FROM suggestions WHERE kind = 'complete' AND target_uuid IN (SELECT uuid FROM nodes WHERE name = 'keeper')"), &status)
	assert.Equal(t, status, database.SuggestApproved, "the live suggestion should approve")
}

// TestSuggestListJSON is the machine-readable face of the review queue.
func TestSuggestListJSON(t *testing.T) {
	_, opts := setupTestEnv(t)

	cmdhelper.RunLflowCmd(t, opts, binaryName, "node", "add", "inbox")
	cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "add", "--parent", "inbox",
		"--author", "agent", "--message", "spotted a gap", "write the migration guide")

	out := cmdhelper.RunLflowCmd(t, opts, binaryName, "suggest", "list", "--format", "json")

	var got []database.Suggestion
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(errors.Wrap(err, "parsing json listing"))
	}
	if len(got) != 1 {
		t.Fatalf("json listing = %d suggestions", len(got))
	}
	assert.Equal(t, got[0].Kind, database.SuggestAdd, "kind mismatch")
	assert.Equal(t, got[0].Name, "write the migration guide", "name mismatch")
	assert.Equal(t, got[0].Author, "agent", "author mismatch")
	assert.Equal(t, got[0].Message, "spotted a gap", "message mismatch")
	assert.Equal(t, got[0].Status, database.SuggestPending, "status mismatch")
}
