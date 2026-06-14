// Package resolve turns node references (uuid, uuid prefix, or free text)
// into nodes using best-match semantics: commands act on the most probable
// node instead of asking. --strict surfaces the alternatives instead.
package resolve

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/pkg/errors"
)

var (
	dim    = color.New(color.FgHiBlack)
	yellow = color.New(color.FgYellow)
	red    = color.New(color.FgRed)
)

// ErrNoMatch is returned when a reference matches no node.
type ErrNoMatch struct{ Ref string }

func (e ErrNoMatch) Error() string { return fmt.Sprintf("no node matching %q", e.Ref) }

// Result is a resolved node along with how many nodes matched in total.
type Result struct {
	Node    database.Node
	Total   int
	Matches []database.Node // all matches, best first (capped)
}

// Resolve resolves ref to the best-matching node. Completed nodes always
// resolve: a reference the user typed is a reference they meant.
func Resolve(db *database.DB, ref string) (Result, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Result{}, ErrNoMatch{Ref: ref}
	}

	// exact uuid
	if n, err := database.GetNode(db, ref); err == nil {
		return Result{Node: n, Total: 1, Matches: []database.Node{n}}, nil
	}

	// uuid prefix (hex-ish, no spaces, >= 6 chars)
	if len(ref) >= 6 && !strings.ContainsAny(ref, " \t") {
		rows, err := db.Query("SELECT uuid FROM nodes WHERE uuid LIKE ? AND deleted = 0 LIMIT 2", ref+"%")
		if err == nil {
			var uuids []string
			for rows.Next() {
				var u string
				if err := rows.Scan(&u); err == nil {
					uuids = append(uuids, u)
				}
			}
			rows.Close()
			if len(uuids) == 1 {
				n, err := database.GetNode(db, uuids[0])
				if err == nil {
					return Result{Node: n, Total: 1, Matches: []database.Node{n}}, nil
				}
			}
		}
	}

	// free-text search, best match first
	matches, err := database.SearchNodes(db, ref, true)
	if err != nil {
		return Result{}, errors.Wrap(err, "searching nodes")
	}
	if len(matches) == 0 {
		return Result{}, ErrNoMatch{Ref: ref}
	}

	return Result{Node: matches[0], Total: len(matches), Matches: matches}, nil
}

// Feedback prints the standard best-match feedback line:
// action text muted gray, node name yellow, alternates noted in the same line.
func Feedback(action string, r Result) {
	line := dim.Sprintf("→ %s ", action) + yellow.Sprintf("%q", r.Node.Name)
	if r.Total > 1 {
		line += dim.Sprintf("  · best of %d · --strict lists instead", r.Total)
	}
	fmt.Println(line)
}

// PrintNoMatch prints the standard miss output (red arrow, dim hint).
func PrintNoMatch(ref string) {
	fmt.Println(red.Sprint("→ ") + fmt.Sprintf("no node matching %s", yellow.Sprintf("%q", ref)))
	fmt.Println(dim.Sprint("  hint: lflow node list shows ids · references match by id, id prefix or text"))
}

// CountNoun formats a count with a singular/plural noun.
func CountNoun(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// PrintMatches lists matches with short ids for --strict.
func PrintMatches(db *database.DB, matches []database.Node) {
	for _, n := range matches {
		count, err := database.CountSubtree(db, n.UUID)
		if err != nil {
			count = 1
		}
		shortID := n.UUID
		if len(shortID) > 6 {
			shortID = shortID[:6]
		}
		fmt.Printf("    %s  %-40s %s\n", dim.Sprint(shortID), n.Name, dim.Sprintf("%s · %s", n.Layout, CountNoun(count, "node")))
	}
}
