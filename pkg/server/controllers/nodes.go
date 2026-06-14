package controllers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/lflow/lflow/pkg/server/app"
	"github.com/lflow/lflow/pkg/server/context"
	"github.com/lflow/lflow/pkg/server/database"
	"github.com/lflow/lflow/pkg/server/operations"
	"github.com/lflow/lflow/pkg/server/presenters"
	"github.com/pkg/errors"
)

// NewNodes creates a new Nodes controller.
func NewNodes(app *app.App) *Nodes {
	return &Nodes{
		app: app,
	}
}

var nodesPerPage = 100

// Nodes is a nodes controller.
type Nodes struct {
	app *app.App
}

// escapeSearchQuery escapes the query for full text search
func escapeSearchQuery(searchQuery string) string {
	return strings.Join(strings.Fields(searchQuery), "&")
}

func parseGetNodesQuery(q url.Values) (app.GetNodesParams, error) {
	pageStr := q.Get("page")
	page := 1
	if len(pageStr) > 0 {
		p, err := strconv.Atoi(pageStr)
		if err != nil || p < 1 {
			return app.GetNodesParams{}, errors.Errorf("invalid page %s", pageStr)
		}
		page = p
	}

	return app.GetNodesParams{
		Page:    page,
		Search:  escapeSearchQuery(q.Get("q")),
		PerPage: nodesPerPage,
	}, nil
}

// GetNodesResponse is a response by V3Index
type GetNodesResponse struct {
	Nodes []presenters.Node `json:"nodes"`
	Total int64             `json:"total"`
}

// V3Index is a v3 handler for listing nodes
func (n *Nodes) V3Index(w http.ResponseWriter, r *http.Request) {
	user := context.User(r.Context())
	if user == nil {
		handleJSONError(w, app.ErrLoginRequired, "getting nodes")
		return
	}

	p, err := parseGetNodesQuery(r.URL.Query())
	if err != nil {
		handleJSONError(w, err, "parsing query")
		return
	}

	result, err := n.app.GetNodes(user.ID, p)
	if err != nil {
		handleJSONError(w, err, "getting nodes")
		return
	}

	respondJSON(w, http.StatusOK, GetNodesResponse{
		Nodes: presenters.PresentNodes(result.Nodes),
		Total: result.Total,
	})
}

// V3Show is api for showing a node
func (n *Nodes) V3Show(w http.ResponseWriter, r *http.Request) {
	user := context.User(r.Context())

	vars := mux.Vars(r)
	nodeUUID := vars["nodeUUID"]

	node, ok, err := operations.GetNode(n.app.DB, nodeUUID, user)
	if !ok {
		handleJSONError(w, app.ErrNotFound, "getting node")
		return
	}
	if err != nil {
		handleJSONError(w, err, "getting node")
		return
	}

	respondJSON(w, http.StatusOK, presenters.PresentNode(node))
}

func (n *Nodes) create(r *http.Request) (database.Node, error) {
	user := context.User(r.Context())
	if user == nil {
		return database.Node{}, app.ErrLoginRequired
	}

	var params app.NodeParams
	if err := parseRequestData(r, &params); err != nil {
		return database.Node{}, errors.Wrap(err, "parsing request payload")
	}

	if err := app.ValidateNodeParams(params); err != nil {
		return database.Node{}, err
	}

	client := getClientType(r)
	node, err := n.app.CreateNode(*user, params, client)
	if err != nil {
		return database.Node{}, errors.Wrap(err, "creating node")
	}

	return node, nil
}

// CreateNodeResp is a response for creating a node
type CreateNodeResp struct {
	Result presenters.Node `json:"result"`
}

// V3Create creates a node
func (n *Nodes) V3Create(w http.ResponseWriter, r *http.Request) {
	node, err := n.create(r)
	if err != nil {
		handleJSONError(w, err, "creating node")
		return
	}

	respondJSON(w, http.StatusCreated, CreateNodeResp{
		Result: presenters.PresentNode(node),
	})
}

func (n *Nodes) update(r *http.Request) (database.Node, error) {
	vars := mux.Vars(r)
	nodeUUID := vars["nodeUUID"]

	user := context.User(r.Context())
	if user == nil {
		return database.Node{}, app.ErrLoginRequired
	}

	var params app.NodeParams
	if err := parseRequestData(r, &params); err != nil {
		return database.Node{}, errors.Wrap(err, "decoding params")
	}

	if err := app.ValidateNodeParams(params); err != nil {
		return database.Node{}, err
	}

	var node database.Node
	if err := n.app.DB.Where("uuid = ? AND user_id = ?", nodeUUID, user.ID).First(&node).Error; err != nil {
		return database.Node{}, errors.Wrap(err, "finding node")
	}

	tx := n.app.DB.Begin()

	node, err := n.app.UpdateNode(tx, *user, node, params)
	if err != nil {
		tx.Rollback()
		return database.Node{}, errors.Wrap(err, "updating node")
	}

	tx.Commit()

	return node, nil
}

type updateNodeResp struct {
	Status int             `json:"status"`
	Result presenters.Node `json:"result"`
}

// V3Update updates a node
func (n *Nodes) V3Update(w http.ResponseWriter, r *http.Request) {
	node, err := n.update(r)
	if err != nil {
		handleJSONError(w, err, "updating node")
		return
	}

	respondJSON(w, http.StatusOK, updateNodeResp{
		Status: http.StatusOK,
		Result: presenters.PresentNode(node),
	})
}

func (n *Nodes) del(r *http.Request) (database.Node, error) {
	vars := mux.Vars(r)
	nodeUUID := vars["nodeUUID"]

	user := context.User(r.Context())
	if user == nil {
		return database.Node{}, app.ErrLoginRequired
	}

	var node database.Node
	if err := n.app.DB.Where("uuid = ? AND user_id = ?", nodeUUID, user.ID).First(&node).Error; err != nil {
		return database.Node{}, errors.Wrap(err, "finding node")
	}

	tx := n.app.DB.Begin()

	node, err := n.app.DeleteNode(tx, *user, node)
	if err != nil {
		tx.Rollback()
		return database.Node{}, errors.Wrap(err, "deleting node")
	}

	tx.Commit()

	return node, nil
}

// DeleteNodeResp is a response for deleting a node
type DeleteNodeResp struct {
	Status int             `json:"status"`
	Result presenters.Node `json:"result"`
}

// V3Delete deletes a node
func (n *Nodes) V3Delete(w http.ResponseWriter, r *http.Request) {
	node, err := n.del(r)
	if err != nil {
		handleJSONError(w, err, "deleting node")
		return
	}

	respondJSON(w, http.StatusOK, DeleteNodeResp{
		Status: http.StatusNoContent,
		Result: presenters.PresentNode(node),
	})
}

// IndexOptions is a handler for OPTIONS endpoint for nodes
func (n *Nodes) IndexOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Version")
}
