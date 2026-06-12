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

package sync

import (
	"fmt"
	"testing"

	"github.com/lflow/lflow/pkg/assert"
	cliDatabase "github.com/lflow/lflow/pkg/cli/database"
	clitest "github.com/lflow/lflow/pkg/cli/testutils"
	"github.com/lflow/lflow/pkg/server/database"
)

func clientNode(t *testing.T, db *cliDatabase.DB, uuid string) cliDatabase.Node {
	t.Helper()
	n, err := cliDatabase.GetNode(db, uuid)
	if err != nil {
		t.Fatalf("getting client node %s: %v", uuid, err)
	}
	return n
}

func serverNodeByName(t *testing.T, env testEnv, name string) database.Node {
	t.Helper()
	var n database.Node
	apitestMustFirst(t, env, &n, "name = ?", name)
	return n
}

func apitestMustFirst(t *testing.T, env testEnv, dest *database.Node, query string, args ...interface{}) {
	t.Helper()
	if err := env.ServerDB.Where(query, args...).First(dest).Error; err != nil {
		t.Fatalf("finding server node %v %v: %v", query, args, err)
	}
}

// TestSyncPushFresh pushes locally created nodes to an empty server.
func TestSyncPushFresh(t *testing.T) {
	env := setupTestEnv(t)
	user := setupUserAndLogin(t, env)

	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "add", "--root", "experiment results")
	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "add", "experiment results", "baseline numbers")
	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "add", "baseline numbers", "parse: 1.42s")

	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

	checkState(t, env.DB, user, env.ServerDB, systemState{
		clientNodeCount:  3,
		clientLastMaxUSN: 3,
		clientLastSyncAt: serverTime.Unix(),
		serverNodeCount:  3,
		serverUserMaxUSN: 3,
	})

	// the parent/child relationship must survive the server-assigned uuids
	root := serverNodeByName(t, env, "experiment results")
	child := serverNodeByName(t, env, "baseline numbers")
	grandchild := serverNodeByName(t, env, "parse: 1.42s")
	assert.Equal(t, child.ParentUUID, root.UUID, "child parent mismatch on server")
	assert.Equal(t, grandchild.ParentUUID, child.UUID, "grandchild parent mismatch on server")

	// client uuids must have been rewritten to the server-assigned ones
	clientChild := clientNode(t, env.DB, child.UUID)
	assert.Equal(t, clientChild.ParentUUID, root.UUID, "client child parent mismatch")
	assert.Equal(t, clientChild.Dirty, false, "client child should be clean after push")
}

// TestSyncPullFresh pulls server nodes into an empty client.
func TestSyncPullFresh(t *testing.T) {
	env := setupTestEnv(t)
	user := setupUserAndLogin(t, env)

	rootUUID := apiCreateNode(t, env, user, "", 0, "reading list", "creating root")
	childUUID := apiCreateNode(t, env, user, rootUUID, 0, "weekend reading", "creating child")

	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

	checkState(t, env.DB, user, env.ServerDB, systemState{
		clientNodeCount:  2,
		clientLastMaxUSN: 2,
		clientLastSyncAt: serverTime.Unix(),
		serverNodeCount:  2,
		serverUserMaxUSN: 2,
	})

	child := clientNode(t, env.DB, childUUID)
	assert.Equal(t, child.ParentUUID, rootUUID, "pulled child parent mismatch")
	assert.Equal(t, child.Name, "weekend reading", "pulled child name mismatch")
	assert.Equal(t, child.Dirty, false, "pulled node should be clean")
}

// TestSyncUpdatePropagation propagates a server-side edit to the client.
func TestSyncUpdatePropagation(t *testing.T) {
	env := setupTestEnv(t)
	user := setupUserAndLogin(t, env)

	rootUUID := apiCreateNode(t, env, user, "", 0, "notes", "creating root")
	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

	apiPatchNode(t, env, user, rootUUID, `{"parent_uuid": "", "rank": 0, "name": "notes v2", "layout": "h1"}`, "updating node")
	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

	n := clientNode(t, env.DB, rootUUID)
	assert.Equal(t, n.Name, "notes v2", "updated name not propagated")
	assert.Equal(t, n.Layout, "h1", "updated layout not propagated")
}

// TestSyncLocalEditPushed propagates a local edit to the server.
func TestSyncLocalEditPushed(t *testing.T) {
	env := setupTestEnv(t)
	user := setupUserAndLogin(t, env)

	rootUUID := apiCreateNode(t, env, user, "", 0, "draft", "creating root")
	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

	// edit locally via the CLI
	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "append", "draft", "first line")
	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

	var serverChild database.Node
	apitestMustFirst(t, env, &serverChild, "name = ?", "first line")
	assert.Equal(t, serverChild.ParentUUID, rootUUID, "server child parent mismatch")
}

// TestSyncDeletePropagation propagates deletions both ways.
func TestSyncDeletePropagation(t *testing.T) {
	t.Run("server to client", func(t *testing.T) {
		env := setupTestEnv(t)
		user := setupUserAndLogin(t, env)

		rootUUID := apiCreateNode(t, env, user, "", 0, "to delete", "creating root")
		clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

		apiDeleteNode(t, env, user, rootUUID, "deleting node")
		clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

		var count int
		cliDatabase.MustScan(t, "counting client nodes", env.DB.QueryRow("SELECT count(*) FROM nodes"), &count)
		assert.Equal(t, count, 0, "deleted node should be expunged from client")
	})

	t.Run("client to server", func(t *testing.T) {
		env := setupTestEnv(t)
		user := setupUserAndLogin(t, env)

		apiCreateNode(t, env, user, "", 0, "doomed", "creating root")
		clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

		clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "rm", "-f", "doomed")
		clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

		var serverNode database.Node
		apitestMustFirst(t, env, &serverNode, "name = ?", "")
		assert.Equal(t, serverNode.Deleted, true, "server node should be tombstoned")

		var count int
		cliDatabase.MustScan(t, "counting client nodes", env.DB.QueryRow("SELECT count(*) FROM nodes"), &count)
		assert.Equal(t, count, 0, "client node should be expunged after pushing delete")
	})
}

// TestSyncDirtyLocalWinsConvergence: when both sides changed, the local dirty
// edit survives the pull and is pushed back, making the server converge.
func TestSyncDirtyLocalWinsConvergence(t *testing.T) {
	env := setupTestEnv(t)
	user := setupUserAndLogin(t, env)

	rootUUID := apiCreateNode(t, env, user, "", 0, "contested", "creating root")
	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

	// remote edit
	apiPatchNode(t, env, user, rootUUID, `{"parent_uuid": "", "rank": 0, "name": "remote edit"}`, "remote edit")
	// local edit (marks dirty)
	cliDatabase.MustExec(t, "local edit", env.DB, "UPDATE nodes SET name = 'local edit', dirty = 1 WHERE uuid = ?", rootUUID)

	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

	// local wins on the client...
	n := clientNode(t, env.DB, rootUUID)
	assert.Equal(t, n.Name, "local edit", "dirty local edit should survive the pull")
	assert.Equal(t, n.Dirty, false, "node should be clean after convergence")

	// ...and the server converges to the local value
	var serverNode database.Node
	apitestMustFirst(t, env, &serverNode, "uuid = ?", rootUUID)
	assert.Equal(t, serverNode.Name, "local edit", "server should converge to the local edit")
}

// TestSyncFull exercises --full sync.
func TestSyncFull(t *testing.T) {
	env := setupTestEnv(t)
	user := setupUserAndLogin(t, env)

	apiCreateNode(t, env, user, "", 0, "a", "creating a")
	apiCreateNode(t, env, user, "", 1, "b", "creating b")

	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync", "-f")

	checkState(t, env.DB, user, env.ServerDB, systemState{
		clientNodeCount:  2,
		clientLastMaxUSN: 2,
		clientLastSyncAt: serverTime.Unix(),
		serverNodeCount:  2,
		serverUserMaxUSN: 2,
	})
}

// TestSyncEmptyServerUpload covers switching to an empty server and uploading.
func TestSyncEmptyServerUpload(t *testing.T) {
	env := setupTestEnv(t)
	setupUserAndLogin(t, env)

	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "add", "--root", "keep me")
	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

	// switch to a brand new empty server
	switchToEmptyServer(t, &env)
	newUser := setupUserAndLogin(t, env)

	out := clitest.MustWaitDnoteCmd(t, env.CmdOpts, clitest.UserConfirmEmptyServerSync, cliBinaryName, "sync")
	_ = out

	var serverNodeCount int64
	if err := env.ServerDB.Model(&database.Node{}).Count(&serverNodeCount).Error; err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, serverNodeCount, int64(1), fmt.Sprintf("node should be uploaded to the new server (user %d)", newUser.ID))
}

// TestSyncIdempotent: re-running sync with no changes is a no-op.
func TestSyncIdempotent(t *testing.T) {
	env := setupTestEnv(t)
	user := setupUserAndLogin(t, env)

	apiCreateNode(t, env, user, "", 0, "stable", "creating node")
	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")
	clitest.RunDnoteCmd(t, env.CmdOpts, cliBinaryName, "sync")

	checkState(t, env.DB, user, env.ServerDB, systemState{
		clientNodeCount:  1,
		clientLastMaxUSN: 1,
		clientLastSyncAt: serverTime.Unix(),
		serverNodeCount:  1,
		serverUserMaxUSN: 1,
	})
}
