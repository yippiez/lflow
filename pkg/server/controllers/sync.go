package controllers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lflow/lflow/pkg/server/context"
	"github.com/lflow/lflow/pkg/server/database"
	"github.com/lflow/lflow/pkg/server/log"
	"github.com/lflow/lflow/pkg/server/middleware"
	"github.com/pkg/errors"

	"github.com/lflow/lflow/pkg/server/app"
)

// NewSync creates a new Sync controller
func NewSync(app *app.App) *Sync {
	return &Sync{
		app: app,
	}
}

// Sync is a sync controller.
type Sync struct {
	app *app.App
}

// fullSyncBefore is the system-wide timestamp that represents the point in time
// before which clients must perform a full-sync rather than incremental sync.
const fullSyncBefore = 0

// SyncFragment contains a piece of information about the server's state.
// It is used to transfer the server's state to the client gradually without having to
// transfer the whole state at once.
type SyncFragment struct {
	FragMaxUSN    int            `json:"frag_max_usn"`
	UserMaxUSN    int            `json:"user_max_usn"`
	CurrentTime   int64          `json:"current_time"`
	Nodes         []SyncFragNode `json:"nodes"`
	ExpungedNodes []string       `json:"expunged_nodes"`
}

// SyncFragNode represents a node in a sync fragment and contains only the
// necessary information for the client to sync the node locally
type SyncFragNode struct {
	UUID        string    `json:"uuid"`
	ParentUUID  string    `json:"parent_uuid"`
	Rank        int       `json:"rank"`
	Name        string    `json:"name"`
	Note        string    `json:"note"`
	Layout      string    `json:"layout"`
	MirrorOf    string    `json:"mirror_of"`
	CompletedAt int64     `json:"completed_at"`
	USN         int       `json:"usn"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	AddedOn     int64     `json:"added_on"`
	EditedOn    int64     `json:"edited_on"`
	Deleted     bool      `json:"deleted"`
}

// NewFragNode presents the given node as a SyncFragNode
func NewFragNode(node database.Node) SyncFragNode {
	return SyncFragNode{
		UUID:        node.UUID,
		ParentUUID:  node.ParentUUID,
		Rank:        node.Rank,
		Name:        node.Name,
		Note:        node.Note,
		Layout:      node.Layout,
		MirrorOf:    node.MirrorOf,
		CompletedAt: node.CompletedAt,
		USN:         node.USN,
		CreatedAt:   node.CreatedAt,
		UpdatedAt:   node.UpdatedAt,
		AddedOn:     node.AddedOn,
		EditedOn:    node.EditedOn,
		Deleted:     node.Deleted,
	}
}

type queryParamError struct {
	key     string
	value   string
	message string
}

func (e *queryParamError) Error() string {
	return fmt.Sprintf("invalid query param %s=%s. %s", e.key, e.value, e.message)
}

func (s *Sync) newFragment(userID, userMaxUSN, afterUSN, limit int) (SyncFragment, error) {
	var nodes []database.Node
	if err := s.app.DB.Where("user_id = ? AND usn > ? AND usn <= ?", userID, afterUSN, userMaxUSN).Order("usn ASC").Limit(limit).Find(&nodes).Error; err != nil {
		return SyncFragment{}, errors.Wrap(err, "finding nodes")
	}

	fragNodes := []SyncFragNode{}
	fragExpungedNodes := []string{}

	fragMaxUSN := 0
	for _, node := range nodes {
		fragMaxUSN = node.USN

		if node.Deleted {
			fragExpungedNodes = append(fragExpungedNodes, node.UUID)
		} else {
			fragNodes = append(fragNodes, NewFragNode(node))
		}
	}

	ret := SyncFragment{
		FragMaxUSN:    fragMaxUSN,
		UserMaxUSN:    userMaxUSN,
		CurrentTime:   s.app.Clock.Now().Unix(),
		Nodes:         fragNodes,
		ExpungedNodes: fragExpungedNodes,
	}

	return ret, nil
}

func parseGetSyncFragmentQuery(q url.Values) (afterUSN, limit int, err error) {
	afterUSNStr := q.Get("after_usn")
	limitStr := q.Get("limit")

	if len(afterUSNStr) > 0 {
		afterUSN, err = strconv.Atoi(afterUSNStr)

		if err != nil {
			err = errors.Wrap(err, "invalid after_usn")
			return
		}
	} else {
		afterUSN = 0
	}

	if len(limitStr) > 0 {
		l, e := strconv.Atoi(limitStr)

		if e != nil {
			err = errors.Wrap(e, "invalid limit")
			return
		}

		if l > 100 {
			err = &queryParamError{
				key:     "limit",
				value:   limitStr,
				message: "maximum value is 100",
			}
			return
		}

		limit = l
	} else {
		limit = 100
	}

	return
}

// GetSyncFragmentResp represents a response from GetSyncFragment handler
type GetSyncFragmentResp struct {
	Fragment SyncFragment `json:"fragment"`
}

// GetSyncFragment responds with a sync fragment
func (s *Sync) GetSyncFragment(w http.ResponseWriter, r *http.Request) {
	user := context.User(r.Context())
	if user == nil {
		middleware.DoError(w, "No authenticated user found", nil, http.StatusInternalServerError)
		return
	}

	afterUSN, limit, err := parseGetSyncFragmentQuery(r.URL.Query())
	if err != nil {
		middleware.DoError(w, "parsing query params", err, http.StatusInternalServerError)
		return
	}

	fragment, err := s.newFragment(user.ID, user.MaxUSN, afterUSN, limit)
	if err != nil {
		middleware.DoError(w, "getting fragment", err, http.StatusInternalServerError)
		return
	}

	response := GetSyncFragmentResp{
		Fragment: fragment,
	}
	respondJSON(w, http.StatusOK, response)
}

// GetSyncStateResp represents a response from GetSyncFragment handler
type GetSyncStateResp struct {
	FullSyncBefore int   `json:"full_sync_before"`
	MaxUSN         int   `json:"max_usn"`
	CurrentTime    int64 `json:"current_time"`
}

// GetSyncState responds with a sync fragment
func (s *Sync) GetSyncState(w http.ResponseWriter, r *http.Request) {
	user := context.User(r.Context())
	if user == nil {
		middleware.DoError(w, "No authenticated user found", nil, http.StatusInternalServerError)
		return
	}

	response := GetSyncStateResp{
		FullSyncBefore: int(user.FullSyncBefore),
		MaxUSN:         user.MaxUSN,
		// TODO: exposing server time means we probably shouldn't seed random generator with time?
		CurrentTime: s.app.Clock.Now().Unix(),
	}

	log.WithFields(log.Fields{
		"user_id": user.ID,
		"resp":    response,
	}).Info("getting sync state")

	respondJSON(w, http.StatusOK, response)
}
