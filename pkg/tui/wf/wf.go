// Package wf is the Workflowy integration: a thin client for the official
// Workflowy REST API (https://workflowy.com/api-reference/) plus the translate
// layer that turns Workflowy nodes into lflow node fields. The editor's wf node
// type pulls a Workflowy subtree through this package; pushing edits back is a
// future step, which is why the client already carries the write verbs' shapes
// (ids, priorities) in its types.
package wf

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/lflow/lflow/pkg/tui/database"
)

// DefaultBaseURL is the production API root; tests point BaseURL at a local
// mock server speaking the same JSON.
const DefaultBaseURL = "https://workflowy.com/api/v1"

// Node is one Workflowy node as the API returns it.
type Node struct {
	ID          string   `json:"id"`
	ParentID    string   `json:"parent_id"`
	Name        string   `json:"name"` // may carry inline HTML (<b>, <i>, <a>…)
	Note        string   `json:"note"`
	Priority    float64  `json:"priority"` // sibling sort order, ascending
	Data        NodeData `json:"data"`
	CreatedAt   int64    `json:"createdAt"`  // unix seconds
	ModifiedAt  int64    `json:"modifiedAt"` // unix seconds
	CompletedAt *int64   `json:"completedAt"`
}

// NodeData carries the layout metadata.
type NodeData struct {
	LayoutMode string `json:"layoutMode"` // bullets | todo | h1 | h2 | h3 | code-block | quote-block
}

// Client talks to the Workflowy API. Zero-value plus APIKey is usable; BaseURL
// and HTTP exist so tests inject a mock server.
type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) get(ctx context.Context, path string, into interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+path, nil)
	if err != nil {
		return errors.Wrap(err, "building workflowy request")
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := c.http().Do(req)
	if err != nil {
		return errors.Wrap(err, "calling workflowy")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("workflowy %s: %s (%s)", path, resp.Status, firstLine(string(body)))
	}
	if err := json.Unmarshal(body, into); err != nil {
		return errors.Wrap(err, "decoding workflowy response")
	}
	return nil
}

// GetNode fetches one node by id.
func (c *Client) GetNode(ctx context.Context, id string) (Node, error) {
	var n Node
	err := c.get(ctx, "/nodes/"+id, &n)
	return n, err
}

// ListChildren fetches the direct children of a node, sorted by priority.
func (c *Client) ListChildren(ctx context.Context, parentID string) ([]Node, error) {
	var out struct {
		Nodes []Node `json:"nodes"`
	}
	if err := c.get(ctx, "/nodes?parent_id="+parentID, &out); err != nil {
		return nil, err
	}
	sort.SliceStable(out.Nodes, func(i, j int) bool { return out.Nodes[i].Priority < out.Nodes[j].Priority })
	return out.Nodes, nil
}

// TreeNode is a fetched Workflowy node with its children resolved.
type TreeNode struct {
	Node
	Children []*TreeNode
}

// MaxFetch caps a subtree pull so a mis-pasted account root cannot spiral into
// thousands of API calls; the caller surfaces the truncation.
const MaxFetch = 500

// FetchSubtree pulls the node and its descendants breadth-first, sorted by
// priority at every level. truncated reports whether the MaxFetch cap cut the
// walk short.
func (c *Client) FetchSubtree(ctx context.Context, rootID string) (root *TreeNode, truncated bool, err error) {
	rn, err := c.GetNode(ctx, rootID)
	if err != nil {
		return nil, false, err
	}
	root = &TreeNode{Node: rn}
	count := 1
	queue := []*TreeNode{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if count >= MaxFetch {
			return root, true, nil
		}
		kids, err := c.ListChildren(ctx, cur.ID)
		if err != nil {
			return nil, false, err
		}
		for i := range kids {
			if count >= MaxFetch {
				return root, true, nil
			}
			tn := &TreeNode{Node: kids[i]}
			cur.Children = append(cur.Children, tn)
			queue = append(queue, tn)
			count++
		}
	}
	return root, false, nil
}

// --- translate layer ---------------------------------------------------------

// layoutToType maps a Workflowy layoutMode onto an lflow node type. Unknown
// modes degrade to bullets — the same never-crash posture artifacts take.
var layoutToType = map[string]string{
	"bullets":     database.TypeBullets,
	"todo":        database.TypeTodo,
	"h1":          database.TypeH1,
	"h2":          database.TypeH2,
	"h3":          database.TypeH3,
	"code-block":  database.TypeCode,
	"quote-block": database.TypeQuote,
}

// TypeFor returns the lflow node type for a Workflowy node.
func TypeFor(n Node) string {
	if t, ok := layoutToType[n.Data.LayoutMode]; ok {
		return t
	}
	return database.TypeBullets
}

var reHTMLTag = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)

// PlainName strips Workflowy's inline HTML from a node name — lflow stores no
// markup in text, ever. Anchor tags keep their inner text (the visible label).
func PlainName(s string) string {
	s = reHTMLTag.ReplaceAllString(s, "")
	s = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(s)
	return strings.TrimSpace(s)
}

// reWFRef pulls a Workflowy node id out of a pasted reference: a full API uuid,
// or any workflowy.com URL whose fragment/path ends in an id segment.
var reWFRef = regexp.MustCompile(`[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}|#/([0-9a-zA-Z]+)\b`)

// ParseRef extracts the Workflowy node id from a pasted URL or bare id; ok is
// false when the text holds nothing id-shaped.
func ParseRef(text string) (id string, ok bool) {
	m := reWFRef.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", false
	}
	if m[1] != "" {
		return m[1], true // the #/shortid form
	}
	return m[0], true // a full uuid
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}
