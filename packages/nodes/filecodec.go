package nodes

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/utils"
	"github.com/pkg/errors"
)

// FileCodec is a two-way binding between a source file and a node subtree:
// Parse decomposes file text into nodes under a root, Render serializes the
// subtree back to file text. `lflow file open` holds one codec per supported
// extension — the editor edits the tree, the codec owns the file shape.
//
// Both directions normalize: a first save may reshape the file (markdown
// paragraphs become list items, python re-indents to 4 spaces), after which
// parse→render is idempotent.
type FileCodec interface {
	// Name labels the codec in messages ("markdown", "python").
	Name() string
	// Exts lists the file extensions (with dot) the codec claims.
	Exts() []string
	// Parse writes src as nodes under rootUUID. The root's existing children
	// are expected to be absent (a fresh scratch tree).
	Parse(db *database.DB, rootUUID, src string) error
	// Render serializes rootUUID's children back to file text.
	Render(db *database.DB, rootUUID string) (string, error)
}

// fileCodecs holds the registered codecs, in registration order.
var fileCodecs []FileCodec

// CodecForPath picks the codec claiming the path's extension.
func CodecForPath(path string) (FileCodec, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	for _, c := range fileCodecs {
		for _, e := range c.Exts() {
			if e == ext {
				return c, true
			}
		}
	}
	return nil, false
}

// CodecExts lists every supported extension, for error messages.
func CodecExts() []string {
	var out []string
	for _, c := range fileCodecs {
		out = append(out, c.Exts()...)
	}
	return out
}

// insertFileNode appends one node under parent at the next rank and returns it.
// File text is stored verbatim (raw): a codec round-trip must not chipify.
func insertFileNode(db *database.DB, parentUUID, name, typ string, completed bool) (database.Node, error) {
	rank, err := database.NextRank(db, parentUUID)
	if err != nil {
		return database.Node{}, err
	}
	uuid, err := utils.GenerateUUID()
	if err != nil {
		return database.Node{}, errors.Wrap(err, "generating uuid")
	}
	now := time.Now().UnixNano()
	n := database.Node{
		UUID:       uuid,
		ParentUUID: parentUUID,
		Rank:       rank,
		Name:       name,
		Type:       typ,
		Priority:   database.PriorityUp,
		AddedOn:    now,
		EditedOn:   now,
	}
	if completed {
		n.CompletedAt = time.Now().Unix()
	}
	if err := n.Insert(db); err != nil {
		return database.Node{}, err
	}
	return n, nil
}
