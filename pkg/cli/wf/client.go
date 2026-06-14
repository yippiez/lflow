// Package wf integrates workflowy as a node-level mirror source: anchored
// workflowy nodes are pulled into the local tree and pushed back. The client
// speaks the official workflowy v1 API — Bearer API-key auth, nodes-export for
// reads and the /api/v1/nodes endpoints for writes.
package wf

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// TreeNode is a node in the workflowy tree.
type TreeNode struct {
	ID           string
	Name         string
	Note         string
	Completed    bool
	LastModified int64 // workflowy "lm" clock; only comparable to itself
	Children     []TreeNode
}

// Operation is a single workflowy write.
type Operation struct {
	Type      string `json:"type"` // create | edit | move | delete | complete | uncomplete
	ProjectID string `json:"projectid"`
	ParentID  string `json:"parentid,omitempty"`
	Priority  int    `json:"priority,omitempty"`
	Name      string `json:"name,omitempty"`
	Note      string `json:"description,omitempty"`
}

// Client is the minimal surface lflow needs from a workflowy backend.
type Client interface {
	FetchTree() (TreeNode, error)
	// Push applies ops in order; returns each create op's ProjectID -> the
	// workflowy-assigned node id so the caller can record the mapping.
	Push(ops []Operation) (map[string]string, error)
}

// APIClient talks to the official workflowy v1 API.
type APIClient struct {
	BaseURL string // https://workflowy.com for the real service
	APIKey  string
	HTTP    *http.Client
}

// NewClient creates a client for the v1 API.
func NewClient(baseURL, apiKey string) *APIClient {
	if baseURL == "" {
		baseURL = "https://workflowy.com"
	}
	return &APIClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// do executes an authenticated request. If body is non-nil it is marshalled to
// JSON; if out is non-nil the response body is unmarshalled into it.
func (c *APIClient) do(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return errors.Wrap(err, "marshalling request body")
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return errors.Wrap(err, "constructing request")
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return errors.Wrap(err, "making request")
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "reading response")
	}
	if resp.StatusCode >= 400 {
		return errors.Errorf("workflowy responded %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return errors.Wrap(err, "decoding response")
		}
	}
	return nil
}

// exportNode is the wire format of a node in nodes-export.
type exportNode struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Note       *string `json:"note"`
	ParentID   *string `json:"parent_id"`
	Priority   float64 `json:"priority"`
	Completed  bool    `json:"completed"`
	ModifiedAt int64   `json:"modifiedAt"`
}

// FetchTree implements Client.
func (c *APIClient) FetchTree() (TreeNode, error) {
	var nodes []exportNode
	if err := c.do("GET", "/api/v1/nodes-export", nil, &nodes); err != nil {
		return TreeNode{}, err
	}

	// index children by parent id, preserving export order
	childrenOf := map[string][]exportNode{}
	for _, n := range nodes {
		parent := ""
		if n.ParentID != nil {
			parent = *n.ParentID
		}
		childrenOf[parent] = append(childrenOf[parent], n)
	}

	var build func(parentID string) []TreeNode
	build = func(parentID string) []TreeNode {
		kids := childrenOf[parentID]
		sort.SliceStable(kids, func(i, j int) bool {
			return kids[i].Priority < kids[j].Priority
		})
		out := make([]TreeNode, 0, len(kids))
		for _, n := range kids {
			note := ""
			if n.Note != nil {
				note = *n.Note
			}
			out = append(out, TreeNode{
				ID:           n.ID,
				Name:         n.Name,
				Note:         note,
				Completed:    n.Completed,
				LastModified: n.ModifiedAt,
				Children:     build(n.ID),
			})
		}
		return out
	}

	return TreeNode{ID: "None", Children: build("")}, nil
}

// Push implements Client.
func (c *APIClient) Push(ops []Operation) (map[string]string, error) {
	idMap := map[string]string{}
	resolve := func(id string) string {
		if mapped, ok := idMap[id]; ok {
			return mapped
		}
		return id
	}

	for _, op := range ops {
		switch op.Type {
		case "create":
			body := map[string]any{
				"parent_id": resolve(op.ParentID),
				"name":      op.Name,
				"position":  "bottom",
			}
			if op.Note != "" {
				body["note"] = op.Note
			}
			var res struct {
				ItemID string `json:"item_id"`
			}
			if err := c.do("POST", "/api/v1/nodes", body, &res); err != nil {
				return idMap, err
			}
			idMap[op.ProjectID] = res.ItemID
		case "edit":
			body := map[string]any{"name": op.Name, "note": op.Note}
			if err := c.do("POST", "/api/v1/nodes/"+resolve(op.ProjectID), body, nil); err != nil {
				return idMap, err
			}
		case "move":
			body := map[string]any{"parent_id": resolve(op.ParentID)}
			if err := c.do("POST", "/api/v1/nodes/"+resolve(op.ProjectID)+"/move", body, nil); err != nil {
				return idMap, err
			}
		case "complete":
			if err := c.do("POST", "/api/v1/nodes/"+resolve(op.ProjectID)+"/complete", nil, nil); err != nil {
				return idMap, err
			}
		case "uncomplete":
			if err := c.do("POST", "/api/v1/nodes/"+resolve(op.ProjectID)+"/uncomplete", nil, nil); err != nil {
				return idMap, err
			}
		case "delete":
			if err := c.do("DELETE", "/api/v1/nodes/"+resolve(op.ProjectID), nil, nil); err != nil {
				return idMap, err
			}
		default:
			return idMap, errors.Errorf("unknown operation type %q", op.Type)
		}
	}

	return idMap, nil
}

// FindByID locates a node in the tree by full id or by the 12-char short id
// used in workflowy URLs (suffix match).
func FindByID(root TreeNode, id string) (TreeNode, bool) {
	short := len(id) == 12
	var walk func(n TreeNode) (TreeNode, bool)
	walk = func(n TreeNode) (TreeNode, bool) {
		if n.ID == id || (short && strings.HasSuffix(strings.ReplaceAll(n.ID, "-", ""), id)) {
			return n, true
		}
		for _, c := range n.Children {
			if hit, ok := walk(c); ok {
				return hit, true
			}
		}
		return TreeNode{}, false
	}
	return walk(root)
}

// ParseNodeRef extracts a workflowy node id from a URL like
// https://workflowy.com/#/abc123def456 or returns the input unchanged.
func ParseNodeRef(ref string) string {
	if idx := strings.Index(ref, "#/"); idx >= 0 {
		return strings.Trim(ref[idx+2:], "/")
	}
	return ref
}
