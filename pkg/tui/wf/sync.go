package wf

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/utils"
	"github.com/pkg/errors"
)

// Mirror is an anchored workflowy node.
type Mirror struct {
	NodeUUID   string
	WfID       string
	Anchor     string // anchor uuid: == NodeUUID for anchors, anchor's uuid for mapped descendants
	LastSync   int64
	WfModified int64
}

// GetMirrors returns all anchor mirrors (rows that anchor themselves).
func GetMirrors(db *database.DB) ([]Mirror, error) {
	rows, err := db.Query("SELECT node_uuid, wf_id, anchor, last_sync, wf_modified FROM wf_mirrors WHERE anchor = node_uuid OR anchor = ''")
	if err != nil {
		return nil, errors.Wrap(err, "querying wf mirrors")
	}
	defer rows.Close()

	var ret []Mirror
	for rows.Next() {
		var m Mirror
		if err := rows.Scan(&m.NodeUUID, &m.WfID, &m.Anchor, &m.LastSync, &m.WfModified); err != nil {
			return nil, errors.Wrap(err, "scanning wf mirror")
		}
		ret = append(ret, m)
	}
	return ret, rows.Err()
}

// GetMirror returns the anchor mirror for the given local node.
func GetMirror(db *database.DB, nodeUUID string) (Mirror, error) {
	var m Mirror
	err := db.QueryRow("SELECT node_uuid, wf_id, anchor, last_sync, wf_modified FROM wf_mirrors WHERE node_uuid = ?", nodeUUID).
		Scan(&m.NodeUUID, &m.WfID, &m.Anchor, &m.LastSync, &m.WfModified)
	if err == sql.ErrNoRows {
		return m, errors.Errorf("node %s is not a workflowy mirror", nodeUUID)
	}
	if err != nil {
		return m, errors.Wrap(err, "querying wf mirror")
	}
	return m, nil
}

// CreateMirror anchors a workflowy node to a local node.
func CreateMirror(db *database.DB, nodeUUID, wfID string) error {
	if _, err := db.Exec("INSERT INTO wf_mirrors (node_uuid, wf_id, anchor) VALUES (?, ?, ?)", nodeUUID, wfID, nodeUUID); err != nil {
		return errors.Wrap(err, "inserting wf mirror")
	}
	return nil
}

// RemoveMirror detaches the anchor and all descendant mappings. If drop is
// true the local subtree is tombstoned as well.
func RemoveMirror(db *database.DB, anchorUUID string, drop bool) error {
	if _, err := db.Exec("DELETE FROM wf_mirrors WHERE anchor = ? OR node_uuid = ?", anchorUUID, anchorUUID); err != nil {
		return errors.Wrap(err, "removing wf mappings")
	}
	if drop {
		if _, err := database.MarkSubtreeDeleted(db, anchorUUID); err != nil {
			return err
		}
	}
	return nil
}

// mapping looks up the wf mapping of a local node, if any. lastSync holds
// the node's edited_on (ns) at the time of the last sync, making local-change
// detection an exact comparison.
func mapping(db *database.DB, nodeUUID string) (wfID string, wfModified, lastSync int64, ok bool, err error) {
	scanErr := db.QueryRow("SELECT wf_id, wf_modified, last_sync FROM wf_mirrors WHERE node_uuid = ?", nodeUUID).Scan(&wfID, &wfModified, &lastSync)
	if scanErr == sql.ErrNoRows {
		return "", 0, 0, false, nil
	}
	if scanErr != nil {
		return "", 0, 0, false, errors.Wrap(scanErr, "querying wf mapping")
	}
	return wfID, wfModified, lastSync, true, nil
}

func setMapping(db *database.DB, nodeUUID, wfID, anchorUUID string, wfModified, lastSync int64) error {
	_, err := db.Exec(`INSERT INTO wf_mirrors (node_uuid, wf_id, anchor, last_sync, wf_modified) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(node_uuid) DO UPDATE SET wf_id = excluded.wf_id, anchor = excluded.anchor,
		last_sync = excluded.last_sync, wf_modified = excluded.wf_modified`,
		nodeUUID, wfID, anchorUUID, lastSync, wfModified)
	if err != nil {
		return errors.Wrap(err, "upserting wf mapping")
	}
	return nil
}

// localByWfID returns the local node uuid mapped to a wf id under an anchor.
func localByWfID(db *database.DB, anchorUUID, wfID string) (string, bool, error) {
	var uuid string
	err := db.QueryRow("SELECT node_uuid FROM wf_mirrors WHERE anchor = ? AND wf_id = ?", anchorUUID, wfID).Scan(&uuid)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.Wrap(err, "querying wf mapping by id")
	}
	return uuid, true, nil
}

// Journal records local values overwritten by workflowy (workflowy wins).
type Journal struct {
	Path string
}

func (j Journal) Write(nodeUUID, field, localValue string) {
	if j.Path == "" {
		return
	}
	f, err := os.OpenFile(j.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\t%s\t%q\n", time.Now().Format(time.RFC3339), nodeUUID, field, localValue)
}

// SyncResult summarizes a mirror sync.
type SyncResult struct {
	Pulled    int
	Pushed    int
	Conflicts int
}

// Syncer reconciles anchored mirrors against workflowy.
type Syncer struct {
	DB      *database.DB
	Client  Client
	Journal Journal
}

// Sync pulls then pushes a single anchor. Workflowy wins conflicts; the local
// loser is journaled. now is injected for testability.
func (s *Syncer) Sync(anchorUUID string, now int64) (SyncResult, error) {
	res := SyncResult{}

	m, err := GetMirror(s.DB, anchorUUID)
	if err != nil {
		return res, err
	}

	root, err := s.Client.FetchTree()
	if err != nil {
		return res, errors.Wrap(err, "fetching workflowy tree")
	}

	wfNode, ok := FindByID(root, m.WfID)
	if !ok {
		return res, errors.Errorf("workflowy node %s not found (unmirror or fix the anchor)", m.WfID)
	}

	// pull: workflowy state wins; local-only nodes survive to be pushed
	if err := s.pullNode(wfNode, anchorUUID, anchorUUID, &res); err != nil {
		return res, err
	}

	// push: local nodes without mappings -> create; locally edited -> edit;
	// mapped-but-gone locals were handled by pull (wf wins)
	var pending []pendingMap
	ops, err := s.buildPushOps(anchorUUID, m.WfID, anchorUUID, &res, &pending)
	if err != nil {
		return res, err
	}
	if len(ops) > 0 {
		idMap, err := s.Client.Push(ops)
		if err != nil {
			return res, errors.Wrap(err, "pushing to workflowy")
		}
		for _, p := range pending {
			if id, ok := idMap[p.placeholder]; ok {
				if err := setMapping(s.DB, p.localUUID, id, p.anchorUUID, 0, p.editedOn); err != nil {
					return res, err
				}
			}
		}
	}

	// descendants: remember each node's edited_on so the next sync detects
	// local edits exactly; the anchor row keeps wall-clock time for display
	if _, err := s.DB.Exec(`UPDATE wf_mirrors SET last_sync = COALESCE((SELECT edited_on FROM nodes WHERE nodes.uuid = wf_mirrors.node_uuid), last_sync)
		WHERE anchor = ? AND node_uuid != ?`, anchorUUID, anchorUUID); err != nil {
		return res, errors.Wrap(err, "stamping descendant sync state")
	}
	if _, err := s.DB.Exec("UPDATE wf_mirrors SET last_sync = ? WHERE node_uuid = ?", now, anchorUUID); err != nil {
		return res, errors.Wrap(err, "stamping sync time")
	}

	return res, nil
}

// pullNode reconciles one wf node's children with the local children of
// localUUID. The anchor node itself keeps its local name (it is the handle).
func (s *Syncer) pullNode(wfNode TreeNode, localUUID, anchorUUID string, res *SyncResult) error {
	localChildren, err := database.GetChildren(s.DB, localUUID)
	if err != nil {
		return err
	}

	localMapped := map[string]database.Node{} // wfID -> local node
	for _, lc := range localChildren {
		wfID, _, _, mapped, err := mapping(s.DB, lc.UUID)
		if err != nil {
			return err
		}
		if mapped {
			localMapped[wfID] = lc
		}
	}

	seen := map[string]bool{}
	for rank, wfChild := range wfNode.Children {
		seen[wfChild.ID] = true

		local, exists := localMapped[wfChild.ID]
		if !exists {
			// brand-new from workflowy
			uuid, err := utils.GenerateUUID()
			if err != nil {
				return errors.Wrap(err, "generating uuid")
			}
			completedAt := int64(0)
			if wfChild.Completed {
				completedAt = time.Now().Unix()
			}
			n := database.Node{
				UUID:        uuid,
				ParentUUID:  localUUID,
				Rank:        rank,
				Name:        wfChild.Name,
				Note:        wfChild.Note,
				Type:        database.TypeBullets,
				CompletedAt: completedAt,
				AddedOn:     time.Now().UnixNano(),
				EditedOn:    time.Now().UnixNano(),
				Dirty:       true, // also reaches the lflow server
			}
			if err := n.Insert(s.DB); err != nil {
				return err
			}
			if err := setMapping(s.DB, uuid, wfChild.ID, anchorUUID, wfChild.LastModified, n.EditedOn); err != nil {
				return err
			}
			res.Pulled++
			if err := s.pullNode(wfChild, uuid, anchorUUID, res); err != nil {
				return err
			}
			continue
		}

		// both sides know the node: detect divergence
		_, storedModified, rowLastSync, _, err := mapping(s.DB, local.UUID)
		if err != nil {
			return err
		}
		wfChanged := wfChild.LastModified > storedModified
		localChanged := local.EditedOn > rowLastSync

		wfCompletedAt := local.CompletedAt
		if wfChild.Completed && local.CompletedAt == 0 {
			wfCompletedAt = time.Now().Unix()
		} else if !wfChild.Completed && local.CompletedAt > 0 {
			wfCompletedAt = 0
		}
		wfDiffers := local.Name != wfChild.Name || local.Note != wfChild.Note || wfCompletedAt != local.CompletedAt || local.Rank != rank

		if wfChanged || (!localChanged && wfDiffers) {
			if localChanged {
				// conflict: workflowy wins, journal the local loser
				if local.Name != wfChild.Name {
					s.Journal.Write(local.UUID, "name", local.Name)
				}
				if local.Note != wfChild.Note {
					s.Journal.Write(local.UUID, "note", local.Note)
				}
				res.Conflicts++
			}
			editedOn := time.Now().UnixNano()
			if _, err := s.DB.Exec("UPDATE nodes SET name = ?, note = ?, completed_at = ?, rank = ?, edited_on = ?, dirty = 1 WHERE uuid = ?",
				wfChild.Name, wfChild.Note, wfCompletedAt, rank, editedOn, local.UUID); err != nil {
				return errors.Wrap(err, "applying workflowy state")
			}
			if err := setMapping(s.DB, local.UUID, wfChild.ID, anchorUUID, wfChild.LastModified, editedOn); err != nil {
				return err
			}
			res.Pulled++
		}

		if err := s.pullNode(wfChild, local.UUID, anchorUUID, res); err != nil {
			return err
		}
	}

	// mapped locally but gone from workflowy: workflowy wins, delete locally
	for wfID, local := range localMapped {
		if seen[wfID] {
			continue
		}
		s.Journal.Write(local.UUID, "deleted", local.Name)
		if _, err := database.MarkSubtreeDeleted(s.DB, local.UUID); err != nil {
			return err
		}
		if _, err := s.DB.Exec("DELETE FROM wf_mirrors WHERE node_uuid = ?", local.UUID); err != nil {
			return errors.Wrap(err, "removing stale mapping")
		}
		res.Pulled++
	}

	return nil
}

// pendingMap records a create op whose workflowy id is assigned by the server;
// once Push returns the assigned id the mapping is written.
type pendingMap struct {
	localUUID, placeholder, anchorUUID string
	editedOn                           int64
}

// buildPushOps creates workflowy operations for local-only and locally-edited
// nodes under parentWfID. Created nodes get a placeholder ProjectID and a
// pendingMap entry; the server assigns their real ids on Push.
func (s *Syncer) buildPushOps(localUUID, parentWfID, anchorUUID string, res *SyncResult, pending *[]pendingMap) ([]Operation, error) {
	var ops []Operation

	children, err := database.GetChildren(s.DB, localUUID)
	if err != nil {
		return nil, err
	}

	for rank, child := range children {
		wfID, _, rowLastSync, mapped, err := mapping(s.DB, child.UUID)
		if err != nil {
			return nil, err
		}

		name := child.Name
		if child.MirrorOf != "" {
			// a local ◆ mirror under a wf anchor pushes as its resolved text
			orig, err := database.GetNode(s.DB, child.MirrorOf)
			if err == nil {
				name = orig.Name
			}
		}

		if !mapped {
			placeholder, err := utils.GenerateUUID()
			if err != nil {
				return nil, errors.Wrap(err, "generating placeholder id")
			}
			ops = append(ops, Operation{Type: "create", ProjectID: placeholder, ParentID: parentWfID, Priority: rank, Name: name, Note: child.Note})
			if child.CompletedAt > 0 {
				ops = append(ops, Operation{Type: "complete", ProjectID: placeholder})
			}
			*pending = append(*pending, pendingMap{localUUID: child.UUID, placeholder: placeholder, anchorUUID: anchorUUID, editedOn: child.EditedOn})
			res.Pushed++

			childOps, err := s.buildPushOps(child.UUID, placeholder, anchorUUID, res, pending)
			if err != nil {
				return nil, err
			}
			ops = append(ops, childOps...)
			continue
		}

		if child.EditedOn > rowLastSync {
			ops = append(ops, Operation{Type: "edit", ProjectID: wfID, Name: name, Note: child.Note})
			res.Pushed++
		}

		childOps, err := s.buildPushOps(child.UUID, wfID, anchorUUID, res, pending)
		if err != nil {
			return nil, err
		}
		ops = append(ops, childOps...)
	}

	return ops, nil
}
