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

// Package wf integrates workflowy as a node-level mirror source: anchored
// workflowy nodes are pulled into the local tree and pushed back. The client
// speaks the internal (unofficial) workflowy API — session-cookie auth,
// get_initialization_data for reads and push_and_poll for writes. The public
// v1 API can later implement the same Client interface.
package wf

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	// FetchTree returns the full account tree and the current transaction id.
	FetchTree() (TreeNode, string, error)
	// Push applies operations on top of the given transaction id and returns
	// the new transaction id.
	Push(ops []Operation, txid string) (string, error)
}

// InternalClient talks to the unofficial workflowy API.
type InternalClient struct {
	BaseURL   string // https://workflowy.com for the real service
	SessionID string
	HTTP      *http.Client
}

// NewInternalClient creates a client for the internal API.
func NewInternalClient(baseURL, sessionID string) *InternalClient {
	if baseURL == "" {
		baseURL = "https://workflowy.com"
	}
	return &InternalClient{
		BaseURL:   baseURL,
		SessionID: sessionID,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

// Login exchanges credentials for a session id.
func Login(baseURL, username, password string) (string, error) {
	if baseURL == "" {
		baseURL = "https://workflowy.com"
	}

	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.PostForm(baseURL+"/ajax_login", form)
	if err != nil {
		return "", errors.Wrap(err, "posting login")
	}
	defer resp.Body.Close()

	for _, c := range resp.Cookies() {
		if c.Name == "sessionid" && c.Value != "" {
			return c.Value, nil
		}
	}

	return "", errors.New("login did not yield a session (check credentials; accounts with 2FA need a session id)")
}

// wfItem is the wire format of a node in get_initialization_data.
type wfItem struct {
	ID        string   `json:"id"`
	Name      string   `json:"nm"`
	Note      string   `json:"no"`
	Completed int64    `json:"cp"` // nonzero when completed
	Modified  int64    `json:"lm"`
	Children  []wfItem `json:"ch"`
}

type initData struct {
	ProjectTreeData struct {
		MainProjectTreeInfo struct {
			RootProjectChildren []wfItem `json:"rootProjectChildren"`
			InitialTransaction  string   `json:"initialMostRecentOperationTransactionId"`
		} `json:"mainProjectTreeInfo"`
	} `json:"projectTreeData"`
}

func toTreeNode(it wfItem) TreeNode {
	n := TreeNode{
		ID:           it.ID,
		Name:         it.Name,
		Note:         it.Note,
		Completed:    it.Completed != 0,
		LastModified: it.Modified,
	}
	for _, c := range it.Children {
		n.Children = append(n.Children, toTreeNode(c))
	}
	return n
}

func (c *InternalClient) do(req *http.Request) ([]byte, error) {
	req.AddCookie(&http.Cookie{Name: "sessionid", Value: c.SessionID})

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "making request")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "reading response")
	}
	if resp.StatusCode >= 400 {
		return nil, errors.Errorf("workflowy responded %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// FetchTree implements Client.
func (c *InternalClient) FetchTree() (TreeNode, string, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/get_initialization_data?client_version=21", nil)
	if err != nil {
		return TreeNode{}, "", errors.Wrap(err, "constructing request")
	}

	body, err := c.do(req)
	if err != nil {
		return TreeNode{}, "", err
	}

	var data initData
	if err := json.Unmarshal(body, &data); err != nil {
		return TreeNode{}, "", errors.Wrap(err, "decoding initialization data")
	}

	root := TreeNode{ID: "None"}
	for _, it := range data.ProjectTreeData.MainProjectTreeInfo.RootProjectChildren {
		root.Children = append(root.Children, toTreeNode(it))
	}

	return root, data.ProjectTreeData.MainProjectTreeInfo.InitialTransaction, nil
}

type pushOperation struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

type pushPayload struct {
	MostRecentOperationTransactionID string          `json:"most_recent_operation_transaction_id"`
	Operations                       []pushOperation `json:"operations"`
}

type pushResult struct {
	Results []struct {
		NewMostRecentOperationTransactionID string `json:"new_most_recent_operation_transaction_id"`
		ErrorEncounteredInRemoteOperations  bool   `json:"error_encountered_in_remote_operations"`
	} `json:"results"`
}

func toPushOperation(op Operation) pushOperation {
	data := map[string]interface{}{"projectid": op.ProjectID}
	switch op.Type {
	case "create":
		data["parentid"] = op.ParentID
		data["priority"] = op.Priority
	case "edit":
		data["name"] = op.Name
		data["description"] = op.Note
	case "move":
		data["parentid"] = op.ParentID
		data["priority"] = op.Priority
	}
	return pushOperation{Type: op.Type, Data: data}
}

// Push implements Client.
func (c *InternalClient) Push(ops []Operation, txid string) (string, error) {
	if len(ops) == 0 {
		return txid, nil
	}

	pushOps := make([]pushOperation, 0, len(ops))
	for _, op := range ops {
		pushOps = append(pushOps, toPushOperation(op))
	}

	payload := []pushPayload{{
		MostRecentOperationTransactionID: txid,
		Operations:                       pushOps,
	}}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", errors.Wrap(err, "marshalling push payload")
	}

	form := url.Values{}
	form.Set("client_id", "lflow")
	form.Set("client_version", "21")
	form.Set("push_poll_id", fmt.Sprintf("lflow-%d", time.Now().UnixNano()))
	form.Set("push_poll_data", string(payloadJSON))

	req, err := http.NewRequest("POST", c.BaseURL+"/push_and_poll", strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.Wrap(err, "constructing request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	body, err := c.do(req)
	if err != nil {
		return "", err
	}

	var result pushResult
	if err := json.Unmarshal(body, &result); err != nil {
		return "", errors.Wrap(err, "decoding push result")
	}
	if len(result.Results) == 0 {
		return "", errors.New("push_and_poll returned no results")
	}
	r := result.Results[0]
	if r.ErrorEncounteredInRemoteOperations {
		return "", errors.New("workflowy reported an error applying operations")
	}

	return r.NewMostRecentOperationTransactionID, nil
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
