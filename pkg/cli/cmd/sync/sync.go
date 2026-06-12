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

// Package sync synchronizes the local node tree with a self-hosted
// lflow-server using the USN-based protocol adapted from dnote.
package sync

import (
	"database/sql"
	"fmt"
	"sort"

	"github.com/lflow/lflow/pkg/cli/client"
	"github.com/lflow/lflow/pkg/cli/consts"
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/infra"
	"github.com/lflow/lflow/pkg/cli/log"
	"github.com/lflow/lflow/pkg/cli/migrate"
	"github.com/lflow/lflow/pkg/cli/ui"
	"github.com/lflow/lflow/pkg/cli/upgrade"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var isFullSync bool
var isDryRun bool
var apiEndpointFlag string

// NewCmd returns a new sync command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync nodes with the lflow server",
		RunE:  newRun(ctx),
	}

	f := cmd.Flags()
	f.BoolVarP(&isFullSync, "full", "f", false, "perform a full sync instead of an incremental one")
	f.BoolVar(&isDryRun, "dry-run", false, "show what would be synced without making changes")
	f.StringVar(&apiEndpointFlag, "apiEndpoint", "", "API endpoint to connect to, defaults to the config value")

	return cmd
}

func getLastSyncAt(tx *database.DB) (int, error) {
	var ret int

	if err := database.GetSystem(tx, consts.SystemLastSyncAt, &ret); err != nil {
		return ret, errors.Wrap(err, "querying last sync time")
	}

	return ret, nil
}

func getLastMaxUSN(tx *database.DB) (int, error) {
	var ret int

	if err := database.GetSystem(tx, consts.SystemLastMaxUSN, &ret); err != nil {
		return ret, errors.Wrap(err, "querying last user max_usn")
	}

	return ret, nil
}

// syncList is an aggregation of resources represented in the sync fragments
type syncList struct {
	Nodes          map[string]client.SyncFragNode
	ExpungedNodes  map[string]bool
	MaxUSN         int
	UserMaxUSN     int // Server's actual max USN (for distinguishing empty fragment vs empty server)
	MaxCurrentTime int64
}

func (l syncList) getLength() int {
	return len(l.Nodes) + len(l.ExpungedNodes)
}

// processFragments categorizes items in sync fragments into a sync list.
func processFragments(fragments []client.SyncFragment) (syncList, error) {
	nodes := map[string]client.SyncFragNode{}
	expungedNodes := map[string]bool{}
	var maxUSN int
	var userMaxUSN int
	var maxCurrentTime int64

	for _, fragment := range fragments {
		for _, node := range fragment.Nodes {
			nodes[node.UUID] = node
		}
		for _, uuid := range fragment.ExpungedNodes {
			expungedNodes[uuid] = true
		}

		if fragment.FragMaxUSN > maxUSN {
			maxUSN = fragment.FragMaxUSN
		}
		if fragment.UserMaxUSN > userMaxUSN {
			userMaxUSN = fragment.UserMaxUSN
		}
		if fragment.CurrentTime > maxCurrentTime {
			maxCurrentTime = fragment.CurrentTime
		}
	}

	sl := syncList{
		Nodes:          nodes,
		ExpungedNodes:  expungedNodes,
		MaxUSN:         maxUSN,
		UserMaxUSN:     userMaxUSN,
		MaxCurrentTime: maxCurrentTime,
	}

	return sl, nil
}

// getSyncList gets a list of all sync fragments after the specified usn
// and aggregates them into a syncList data structure
func getSyncList(ctx context.DnoteCtx, afterUSN int) (syncList, error) {
	fragments, err := getSyncFragments(ctx, afterUSN)
	if err != nil {
		return syncList{}, errors.Wrap(err, "getting sync fragments")
	}

	ret, err := processFragments(fragments)
	if err != nil {
		return syncList{}, errors.Wrap(err, "making sync list")
	}

	return ret, nil
}

// getSyncFragments repeatedly gets all sync fragments after the specified usn until there is no more new data
// remaining and returns the buffered list
func getSyncFragments(ctx context.DnoteCtx, afterUSN int) ([]client.SyncFragment, error) {
	var buf []client.SyncFragment

	nextAfterUSN := afterUSN

	for {
		resp, err := client.GetSyncFragment(ctx, nextAfterUSN)
		if err != nil {
			return buf, errors.Wrap(err, "getting sync fragment")
		}

		frag := resp.Fragment
		buf = append(buf, frag)

		nextAfterUSN = frag.FragMaxUSN

		// if there is no more data, break
		if nextAfterUSN == 0 {
			break
		}
	}

	log.Debug("received %d sync fragments\n", len(buf))

	return buf, nil
}

// mergeNode reconciles a server node with the local copy.
//
//   - local not dirty: the server state wins entirely.
//   - local dirty: local content is kept (and stays dirty so it is pushed
//     back); only the server USN is taken. The follow-up send in the sync
//     loop makes the server converge to the local state.
//   - local tombstoned but the server has an update: the server wins and the
//     node is revived (matching dnote's behavior for deleted-locally edits).
func mergeNode(tx *database.DB, serverNode client.SyncFragNode, localNode database.Node) error {
	if localNode.Deleted || !localNode.Dirty {
		if _, err := tx.Exec(`UPDATE nodes SET parent_uuid = ?, rank = ?, name = ?, note = ?, layout = ?,
			mirror_of = ?, completed_at = ?, edited_on = ?, usn = ?, deleted = ?, dirty = 0 WHERE uuid = ?`,
			serverNode.ParentUUID, serverNode.Rank, serverNode.Name, serverNode.Note, serverNode.Layout,
			serverNode.MirrorOf, serverNode.CompletedAt, serverNode.EditedOn, serverNode.USN,
			serverNode.Deleted, serverNode.UUID); err != nil {
			return errors.Wrapf(err, "updating local node %s", serverNode.UUID)
		}
		return nil
	}

	// dirty local change wins; take only the server USN and push later
	if _, err := tx.Exec("UPDATE nodes SET usn = ? WHERE uuid = ?", serverNode.USN, serverNode.UUID); err != nil {
		return errors.Wrapf(err, "updating usn of local node %s", serverNode.UUID)
	}
	return nil
}

func insertServerNode(tx *database.DB, n client.SyncFragNode) error {
	node := database.Node{
		UUID:        n.UUID,
		ParentUUID:  n.ParentUUID,
		Rank:        n.Rank,
		Name:        n.Name,
		Note:        n.Note,
		Layout:      n.Layout,
		MirrorOf:    n.MirrorOf,
		CompletedAt: n.CompletedAt,
		AddedOn:     n.AddedOn,
		EditedOn:    n.EditedOn,
		USN:         n.USN,
		Deleted:     n.Deleted,
		Dirty:       false,
	}
	if err := node.Insert(tx); err != nil {
		return errors.Wrapf(err, "inserting node with uuid %s", n.UUID)
	}
	return nil
}

func getLocalNode(tx *database.DB, uuid string) (database.Node, error) {
	var n database.Node
	err := tx.QueryRow("SELECT uuid, usn, deleted, dirty FROM nodes WHERE uuid = ?", uuid).
		Scan(&n.UUID, &n.USN, &n.Deleted, &n.Dirty)
	return n, err
}

func stepSyncNode(tx *database.DB, n client.SyncFragNode) error {
	localNode, err := getLocalNode(tx, n.UUID)
	if err != nil && err != sql.ErrNoRows {
		return errors.Wrapf(err, "getting local node %s", n.UUID)
	}

	if err == sql.ErrNoRows {
		return insertServerNode(tx, n)
	}

	return mergeNode(tx, n, localNode)
}

func fullSyncNode(tx *database.DB, n client.SyncFragNode) error {
	localNode, err := getLocalNode(tx, n.UUID)
	if err != nil && err != sql.ErrNoRows {
		return errors.Wrapf(err, "getting local node %s", n.UUID)
	}

	if err == sql.ErrNoRows {
		return insertServerNode(tx, n)
	}

	if n.USN > localNode.USN {
		return mergeNode(tx, n, localNode)
	}

	return nil
}

func syncDeleteNode(tx *database.DB, nodeUUID string) error {
	localNode, err := getLocalNode(tx, nodeUUID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "getting local node %s", nodeUUID)
	}

	// if local copy is dirty, keep it; it will be uploaded again later
	if localNode.Dirty {
		return nil
	}

	if err := (database.Node{UUID: nodeUUID}).Expunge(tx); err != nil {
		return errors.Wrapf(err, "deleting local node %s", nodeUUID)
	}

	return nil
}

// cleanLocalNodes deletes from the local database any nodes that are in invalid state
// judging by the full list of resources in the server. The only acceptable situation
// in which a local node is not present in the server is if it is new and has not been
// uploaded (i.e. dirty and usn is 0).
func cleanLocalNodes(tx *database.DB, fullList *syncList) error {
	rows, err := tx.Query("SELECT uuid, usn, dirty FROM nodes")
	if err != nil {
		return errors.Wrap(err, "getting local nodes")
	}
	defer rows.Close()

	var toExpunge []string
	for rows.Next() {
		var node database.Node
		if err := rows.Scan(&node.UUID, &node.USN, &node.Dirty); err != nil {
			return errors.Wrap(err, "scanning a row for local node")
		}

		_, inNodes := fullList.Nodes[node.UUID]
		_, inExpunged := fullList.ExpungedNodes[node.UUID]
		if !inNodes && !inExpunged && (!node.Dirty || node.USN != 0) {
			toExpunge = append(toExpunge, node.UUID)
		}
	}
	if err := rows.Err(); err != nil {
		return errors.Wrap(err, "iterating local nodes")
	}

	for _, uuid := range toExpunge {
		if err := (database.Node{UUID: uuid}).Expunge(tx); err != nil {
			return errors.Wrap(err, "expunging a node")
		}
	}

	return nil
}

func fullSync(ctx context.DnoteCtx, tx *database.DB) error {
	log.Debug("performing a full sync\n")
	log.Info("resolving delta.")

	log.DebugNewline()

	list, err := getSyncList(ctx, 0)
	if err != nil {
		return errors.Wrap(err, "getting sync list")
	}

	fmt.Printf(" (total %d).", list.getLength())

	log.DebugNewline()

	if err := cleanLocalNodes(tx, &list); err != nil {
		return errors.Wrap(err, "cleaning up local nodes")
	}

	for _, node := range list.Nodes {
		if err := fullSyncNode(tx, node); err != nil {
			return errors.Wrap(err, "merging node")
		}
	}

	for nodeUUID := range list.ExpungedNodes {
		if err := syncDeleteNode(tx, nodeUUID); err != nil {
			return errors.Wrap(err, "deleting node")
		}
	}

	err = saveSyncState(tx, list.MaxCurrentTime, list.MaxUSN, list.UserMaxUSN)
	if err != nil {
		return errors.Wrap(err, "saving sync state")
	}

	fmt.Println(" done.")

	return nil
}

func stepSync(ctx context.DnoteCtx, tx *database.DB, afterUSN int) error {
	log.Debug("performing a step sync\n")

	log.Info("resolving delta.")

	log.DebugNewline()

	list, err := getSyncList(ctx, afterUSN)
	if err != nil {
		return errors.Wrap(err, "getting sync list")
	}

	fmt.Printf(" (total %d).", list.getLength())

	for _, node := range list.Nodes {
		if err := stepSyncNode(tx, node); err != nil {
			return errors.Wrap(err, "merging node")
		}
	}

	for nodeUUID := range list.ExpungedNodes {
		if err := syncDeleteNode(tx, nodeUUID); err != nil {
			return errors.Wrap(err, "deleting node")
		}
	}

	err = saveSyncState(tx, list.MaxCurrentTime, list.MaxUSN, list.UserMaxUSN)
	if err != nil {
		return errors.Wrap(err, "saving sync state")
	}

	fmt.Println(" done.")

	return nil
}

// dirtyNode is a node queued for upload, annotated with its depth so that
// parents are created on the server before their children.
type dirtyNode struct {
	node  database.Node
	depth int
}

func getDirtyNodes(tx *database.DB) ([]dirtyNode, error) {
	rows, err := tx.Query("SELECT uuid, parent_uuid, rank, name, note, layout, mirror_of, completed_at, added_on, edited_on, usn, deleted, dirty FROM nodes WHERE dirty AND uuid != ?", database.RootUUID)
	if err != nil {
		return nil, errors.Wrap(err, "getting syncable nodes")
	}
	defer rows.Close()

	var dirty []database.Node
	for rows.Next() {
		var n database.Node
		if err = rows.Scan(&n.UUID, &n.ParentUUID, &n.Rank, &n.Name, &n.Note, &n.Layout, &n.MirrorOf,
			&n.CompletedAt, &n.AddedOn, &n.EditedOn, &n.USN, &n.Deleted, &n.Dirty); err != nil {
			return nil, errors.Wrap(err, "scanning a syncable node")
		}
		dirty = append(dirty, n)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterating syncable nodes")
	}

	// compute depths so parents sort before children
	depthCache := map[string]int{"": 0}
	var depthOf func(uuid string, hops int) int
	depthOf = func(uuid string, hops int) int {
		if uuid == "" {
			return 0
		}
		if d, ok := depthCache[uuid]; ok {
			return d
		}
		if hops > 1000 { // cycle guard
			return hops
		}
		var parent string
		err := tx.QueryRow("SELECT parent_uuid FROM nodes WHERE uuid = ?", uuid).Scan(&parent)
		if err != nil {
			depthCache[uuid] = 0
			return 0
		}
		d := depthOf(parent, hops+1) + 1
		depthCache[uuid] = d
		return d
	}

	ret := make([]dirtyNode, 0, len(dirty))
	for _, n := range dirty {
		ret = append(ret, dirtyNode{node: n, depth: depthOf(n.UUID, 0)})
	}
	sort.SliceStable(ret, func(i, j int) bool { return ret[i].depth < ret[j].depth })

	return ret, nil
}

func nodePayload(n database.Node) client.NodePayload {
	return client.NodePayload{
		ParentUUID:  n.ParentUUID,
		Rank:        n.Rank,
		Name:        n.Name,
		Note:        n.Note,
		Layout:      n.Layout,
		MirrorOf:    n.MirrorOf,
		CompletedAt: n.CompletedAt,
		AddedOn:     n.AddedOn,
		EditedOn:    n.EditedOn,
	}
}

func sendNodes(ctx context.DnoteCtx, tx *database.DB) (bool, error) {
	isBehind := false

	dirty, err := getDirtyNodes(tx)
	if err != nil {
		return isBehind, err
	}

	for _, dn := range dirty {
		node := dn.node

		// re-read: an earlier UpdateUUID may have rewritten this node's parent
		fresh, err := database.GetNode(tx, node.UUID)
		if err == nil {
			node = fresh
		}

		log.Debug("sending node %s\n", node.UUID)

		var respUSN int

		if node.USN == 0 {
			if node.Deleted {
				// added and deleted locally without ever reaching the server
				if err := node.Expunge(tx); err != nil {
					return isBehind, errors.Wrap(err, "expunging a node locally")
				}
				continue
			}

			resp, err := client.CreateNode(ctx, nodePayload(node))
			if err != nil {
				log.Debug("error creating node (will retry after stepSync): %v\n", err)
				isBehind = true
				continue
			}

			node.Dirty = false
			node.USN = resp.Result.USN
			if err := node.Update(tx); err != nil {
				return isBehind, errors.Wrap(err, "marking node clean")
			}
			if err := node.UpdateUUID(tx, resp.Result.UUID); err != nil {
				return isBehind, errors.Wrap(err, "updating node uuid")
			}

			respUSN = resp.Result.USN
		} else if node.Deleted {
			resp, err := client.DeleteNode(ctx, node.UUID)
			if err != nil {
				return isBehind, errors.Wrap(err, "deleting a node")
			}

			if err := node.Expunge(tx); err != nil {
				return isBehind, errors.Wrap(err, "expunging a node locally")
			}

			respUSN = resp.Result.USN
		} else {
			resp, err := client.UpdateNode(ctx, node.UUID, nodePayload(node))
			if err != nil {
				return isBehind, errors.Wrap(err, "updating a node")
			}

			node.Dirty = false
			node.USN = resp.Result.USN
			if err := node.Update(tx); err != nil {
				return isBehind, errors.Wrap(err, "marking node clean")
			}

			respUSN = resp.Result.USN
		}

		lastMaxUSN, err := getLastMaxUSN(tx)
		if err != nil {
			return isBehind, errors.Wrap(err, "getting last max usn")
		}

		log.Debug("sent node %s. response USN %d. last max usn: %d\n", node.UUID, respUSN, lastMaxUSN)

		if respUSN == lastMaxUSN+1 {
			if err := updateLastMaxUSN(tx, lastMaxUSN+1); err != nil {
				return isBehind, errors.Wrap(err, "updating last max usn")
			}
		} else {
			isBehind = true
		}
	}

	return isBehind, nil
}

func sendChanges(ctx context.DnoteCtx, tx *database.DB) (bool, error) {
	log.Info("sending changes.")

	var delta int
	if err := tx.QueryRow("SELECT count(*) FROM nodes WHERE dirty").Scan(&delta); err != nil {
		return false, errors.Wrap(err, "counting dirty nodes")
	}

	fmt.Printf(" (total %d).", delta)

	log.DebugNewline()

	isBehind, err := sendNodes(ctx, tx)
	if err != nil {
		return isBehind, errors.Wrap(err, "sending nodes")
	}

	fmt.Println(" done.")

	return isBehind, nil
}

func updateLastMaxUSN(tx *database.DB, val int) error {
	if err := database.UpdateSystem(tx, consts.SystemLastMaxUSN, val); err != nil {
		return errors.Wrapf(err, "updating %s", consts.SystemLastMaxUSN)
	}

	return nil
}

func updateLastSyncAt(tx *database.DB, val int64) error {
	if err := database.UpdateSystem(tx, consts.SystemLastSyncAt, val); err != nil {
		return errors.Wrapf(err, "updating %s", consts.SystemLastSyncAt)
	}

	return nil
}

func saveSyncState(tx *database.DB, serverTime int64, serverMaxUSN int, userMaxUSN int) error {
	// Handle last_max_usn update based on server state:
	// - If serverMaxUSN > 0: we got data, update to serverMaxUSN
	// - If serverMaxUSN == 0 && userMaxUSN > 0: empty fragment (caught up), preserve existing
	// - If serverMaxUSN == 0 && userMaxUSN == 0: empty server, reset to 0
	if serverMaxUSN > 0 {
		if err := updateLastMaxUSN(tx, serverMaxUSN); err != nil {
			return errors.Wrap(err, "updating last max usn")
		}
	} else if userMaxUSN == 0 {
		if err := updateLastMaxUSN(tx, 0); err != nil {
			return errors.Wrap(err, "updating last max usn")
		}
	}

	// Always update last_sync_at (we did communicate with server)
	if err := updateLastSyncAt(tx, serverTime); err != nil {
		return errors.Wrap(err, "updating last sync at")
	}

	return nil
}

// prepareEmptyServerSync marks all local nodes as dirty when syncing to an empty server.
func prepareEmptyServerSync(tx *database.DB) error {
	if _, err := tx.Exec("UPDATE nodes SET usn = 0, dirty = 1 WHERE deleted = 0 AND uuid != ?", database.RootUUID); err != nil {
		return errors.Wrap(err, "marking nodes as dirty")
	}

	if err := updateLastMaxUSN(tx, 0); err != nil {
		return errors.Wrap(err, "resetting last max usn")
	}

	return nil
}

func dryRun(ctx context.DnoteCtx, tx *database.DB) error {
	var pushCount int
	if err := tx.QueryRow("SELECT count(*) FROM nodes WHERE dirty").Scan(&pushCount); err != nil {
		return errors.Wrap(err, "counting dirty nodes")
	}

	lastMaxUSN, err := getLastMaxUSN(tx)
	if err != nil {
		return err
	}

	syncState, err := client.GetSyncState(ctx)
	if err != nil {
		return errors.Wrap(err, "getting the sync state from the server")
	}

	pullCount := 0
	if syncState.MaxUSN > lastMaxUSN {
		pullCount = syncState.MaxUSN - lastMaxUSN
	}

	log.Plainf("  would push %d · pull ~%d (usn %d → %d)\n", pushCount, pullCount, lastMaxUSN, syncState.MaxUSN)

	return nil
}

func newRun(ctx context.DnoteCtx) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		// Override APIEndpoint if flag was provided
		if apiEndpointFlag != "" {
			ctx.APIEndpoint = apiEndpointFlag
		}

		if ctx.SessionKey == "" {
			return errors.New("not logged in")
		}

		if err := migrate.Run(ctx, migrate.RemoteSequence, migrate.RemoteMode); err != nil {
			return errors.Wrap(err, "running remote migrations")
		}

		tx, err := ctx.DB.Begin()
		if err != nil {
			return errors.Wrap(err, "beginning a transaction")
		}

		if isDryRun {
			if err := dryRun(ctx, tx); err != nil {
				tx.Rollback()
				return err
			}
			tx.Rollback()
			return nil
		}

		syncState, err := client.GetSyncState(ctx)
		if err != nil {
			return errors.Wrap(err, "getting the sync state from the server")
		}
		lastSyncAt, err := getLastSyncAt(tx)
		if err != nil {
			return errors.Wrap(err, "getting the last sync time")
		}
		lastMaxUSN, err := getLastMaxUSN(tx)
		if err != nil {
			return errors.Wrap(err, "getting the last max_usn")
		}

		log.Debug("lastSyncAt: %d, lastMaxUSN: %d, syncState: %+v\n", lastSyncAt, lastMaxUSN, syncState)

		var nodeCount int
		if err := tx.QueryRow("SELECT count(*) FROM nodes WHERE deleted = 0 AND uuid != ?", database.RootUUID).Scan(&nodeCount); err != nil {
			return errors.Wrap(err, "counting local nodes")
		}

		// If a client has previously synced (lastMaxUSN > 0) but the server was never synced to (MaxUSN = 0),
		// and the client has undeleted nodes, allow uploading all data to the server.
		if syncState.MaxUSN == 0 && lastMaxUSN > 0 && nodeCount > 0 {
			log.Warnf("The server is empty but you have local data. Maybe you switched servers?\n")

			confirmed, err := ui.Confirm(fmt.Sprintf("Upload %d nodes to the server?", nodeCount), false)
			if err != nil {
				tx.Rollback()
				return errors.Wrap(err, "getting user confirmation")
			}

			if !confirmed {
				tx.Rollback()
				return errors.New("sync cancelled by user")
			}

			fmt.Println()

			if err := prepareEmptyServerSync(tx); err != nil {
				return errors.Wrap(err, "preparing for empty server sync")
			}

			lastMaxUSN, err = getLastMaxUSN(tx)
			if err != nil {
				return errors.Wrap(err, "getting the last max_usn after prepare")
			}
		}

		// If full sync will be triggered by FullSyncBefore (not manual --full flag),
		// and client has more data than server, prepare local data for upload.
		if !isFullSync && lastSyncAt < syncState.FullSyncBefore && lastMaxUSN > syncState.MaxUSN {
			log.Debug("full sync triggered by FullSyncBefore: preparing local data for upload\n")

			if err := prepareEmptyServerSync(tx); err != nil {
				return errors.Wrap(err, "preparing local data for full sync")
			}

			lastMaxUSN, err = getLastMaxUSN(tx)
			if err != nil {
				return errors.Wrap(err, "getting the last max_usn after prepare")
			}
		}

		var syncErr error
		if isFullSync || lastSyncAt < syncState.FullSyncBefore {
			syncErr = fullSync(ctx, tx)
		} else if lastMaxUSN != syncState.MaxUSN {
			syncErr = stepSync(ctx, tx, lastMaxUSN)
		} else {
			// if no need to sync from the server, simply update the last sync timestamp and proceed to send changes
			err = updateLastSyncAt(tx, syncState.CurrentTime)
			if err != nil {
				return errors.Wrap(err, "updating last sync at")
			}
		}
		if syncErr != nil {
			tx.Rollback()
			return errors.Wrap(syncErr, "syncing changes from the server")
		}

		isBehind, err := sendChanges(ctx, tx)
		if err != nil {
			tx.Rollback()
			return errors.Wrap(err, "sending changes")
		}

		// if server state gets ahead of that of client during the sync, do an additional step sync
		if isBehind {
			log.Debug("performing another step sync because client is behind\n")

			updatedLastMaxUSN, err := getLastMaxUSN(tx)
			if err != nil {
				tx.Rollback()
				return errors.Wrap(err, "getting the new last max_usn")
			}

			err = stepSync(ctx, tx, updatedLastMaxUSN)
			if err != nil {
				tx.Rollback()
				return errors.Wrap(err, "performing the follow-up step sync")
			}

			// After syncing server changes, send local changes again
			_, err = sendChanges(ctx, tx)
			if err != nil {
				tx.Rollback()
				return errors.Wrap(err, "sending changes after conflict resolution")
			}
		}

		if err := tx.Commit(); err != nil {
			return errors.Wrap(err, "committing transaction")
		}

		log.Success("success\n")

		if err := upgrade.Check(ctx); err != nil {
			log.Error(errors.Wrap(err, "automatically checking updates").Error())
		}

		return nil
	}
}
