package database

import (
	"sort"
	"strings"
	"time"

	"github.com/lflow/lflow/packages/utils"
	"github.com/pkg/errors"
)

// Kind values for a suggestion: what the author proposes.
const (
	// SuggestAdd proposes a new node under TargetUUID (the parent).
	SuggestAdd = "add"
	// SuggestEdit proposes new field values for the node at TargetUUID.
	SuggestEdit = "edit"
	// SuggestComplete and SuggestUncomplete propose changing the node's done state.
	SuggestComplete   = "complete"
	SuggestUncomplete = "uncomplete"
)

// Status values for a suggestion. A suggestion starts pending and settles
// exactly once — approving applies it to the tree, rejecting never does.
const (
	SuggestPending  = "pending"
	SuggestApproved = "approved"
	SuggestRejected = "rejected"
)

// Fields an edit suggestion can propose. Which ones a suggestion actually
// carries lives in Fields, so proposing an empty note (clearing it) reads
// differently from not proposing a note at all.
const (
	FieldName = "name"
	FieldNote = "note"
	FieldType = "type"
)

// Position values for an add suggestion: where the proposed node lands among
// its siblings on approval. Empty defers to the parent's own priority.
const (
	PositionTop    = "top"
	PositionBottom = "bottom"
)

// Suggestion is a proposed change to the outline, held outside the tree until
// somebody approves it. Nothing here renders or syncs as a node: the row is
// inert data, and only ApplySuggestion turns it into an actual write.
//
// WARNING (invariant): a suggestion never mutates the tree on its own. Every
// path that changes nodes from a suggestion runs through ApplySuggestion, so a
// pending or rejected suggestion is guaranteed to have touched nothing.
type Suggestion struct {
	UUID string `json:"uuid"`
	Kind string `json:"kind"`
	// TargetUUID is the node the suggestion is about: the parent to add under
	// (add), or the node to change (edit).
	TargetUUID string `json:"target_uuid"`
	Name       string `json:"name"`
	Note       string `json:"note"`
	Type       string `json:"type"`
	// Fields lists which of name/note/type an edit proposes, comma separated.
	Fields   string `json:"fields"`
	Position string `json:"position"`
	// Raw stores the proposed text verbatim: no #tag, date or link chips are
	// created when it is applied.
	Raw bool `json:"raw"`
	// BaseName and BaseNote snapshot the target when the suggestion was made,
	// so a reviewer sees whether the node drifted underneath it.
	BaseName   string `json:"base_name"`
	BaseNote   string `json:"base_note"`
	Author     string `json:"author"`
	Message    string `json:"message"`
	Status     string `json:"status"`
	CreatedOn  int64  `json:"created_on"`
	ResolvedOn int64  `json:"resolved_on"`
	// ResultUUID is the node an approved add suggestion created.
	ResultUUID string `json:"result_uuid"`
}

const suggestionColumns = "uuid, kind, target_uuid, name, note, type, fields, position, raw, " +
	"base_name, base_note, author, message, status, created_on, resolved_on, result_uuid"

func scanSuggestion(row interface{ Scan(...interface{}) error }) (Suggestion, error) {
	var s Suggestion
	err := row.Scan(&s.UUID, &s.Kind, &s.TargetUUID, &s.Name, &s.Note, &s.Type, &s.Fields,
		&s.Position, &s.Raw, &s.BaseName, &s.BaseNote, &s.Author, &s.Message, &s.Status,
		&s.CreatedOn, &s.ResolvedOn, &s.ResultUUID)
	return s, err
}

// ErrNoSuggestion is returned when a reference matches no suggestion.
type ErrNoSuggestion struct{ Ref string }

func (e ErrNoSuggestion) Error() string { return "no suggestion matching " + e.Ref }

// ErrAmbiguousSuggestion is returned when an id prefix matches several
// suggestions; the caller prints the candidates and asks for a longer prefix.
type ErrAmbiguousSuggestion struct {
	Ref     string
	Matches []Suggestion
}

func (e ErrAmbiguousSuggestion) Error() string {
	return "several suggestions match " + e.Ref
}

// FieldList splits an edit suggestion's proposed-field list.
func (s Suggestion) FieldList() []string {
	if s.Fields == "" {
		return nil
	}
	return strings.Split(s.Fields, ",")
}

// Proposes reports whether the suggestion carries a value for the field.
func (s Suggestion) Proposes(field string) bool {
	for _, f := range s.FieldList() {
		if f == field {
			return true
		}
	}
	return false
}

// Pending reports whether the suggestion is still awaiting review.
func (s Suggestion) Pending() bool { return s.Status == SuggestPending }

// Insert stores the suggestion. Callers that leave UUID or CreatedOn empty get
// a generated id and the current time.
func (s *Suggestion) Insert(db *DB) error {
	if s.UUID == "" {
		uuid, err := utils.GenerateUUID()
		if err != nil {
			return errors.Wrap(err, "generating suggestion uuid")
		}
		s.UUID = uuid
	}
	if s.Status == "" {
		s.Status = SuggestPending
	}
	if s.CreatedOn == 0 {
		s.CreatedOn = time.Now().UnixNano()
	}
	_, err := db.Exec("INSERT INTO suggestions ("+suggestionColumns+
		") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		s.UUID, s.Kind, s.TargetUUID, s.Name, s.Note, s.Type, s.Fields, s.Position, s.Raw,
		s.BaseName, s.BaseNote, s.Author, s.Message, s.Status, s.CreatedOn, s.ResolvedOn, s.ResultUUID)
	if err != nil {
		return errors.Wrapf(err, "inserting suggestion %s", s.UUID)
	}
	return nil
}

// GetSuggestion loads one suggestion by exact uuid.
func GetSuggestion(db *DB, uuid string) (Suggestion, error) {
	row := db.QueryRow("SELECT "+suggestionColumns+" FROM suggestions WHERE uuid = ?", uuid)
	s, err := scanSuggestion(row)
	if err != nil {
		return Suggestion{}, errors.Wrapf(err, "getting suggestion %s", uuid)
	}
	return s, nil
}

// SuggestionFilter narrows a listing. An empty field does not filter.
type SuggestionFilter struct {
	Status     string // pending/approved/rejected; "" or "all" for every status
	TargetUUID string
	Kind       string
}

// ListSuggestions returns matching suggestions, newest last so a terminal
// listing reads in the order they were made.
func ListSuggestions(db *DB, f SuggestionFilter) ([]Suggestion, error) {
	var conds []string
	var args []interface{}
	if f.Status != "" && f.Status != "all" {
		conds = append(conds, "status = ?")
		args = append(args, f.Status)
	}
	if f.TargetUUID != "" {
		conds = append(conds, "target_uuid = ?")
		args = append(args, f.TargetUUID)
	}
	if f.Kind != "" {
		conds = append(conds, "kind = ?")
		args = append(args, f.Kind)
	}
	q := "SELECT " + suggestionColumns + " FROM suggestions"
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY created_on, uuid"

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, errors.Wrap(err, "querying suggestions")
	}
	defer rows.Close()

	var out []Suggestion
	for rows.Next() {
		s, err := scanSuggestion(rows)
		if err != nil {
			return nil, errors.Wrap(err, "scanning suggestion")
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ResolveSuggestion turns a reference — a full uuid or an id prefix — into one
// suggestion. Prefixes are how the CLI refers to suggestions, mirroring the
// short ids `lflow node list` prints.
func ResolveSuggestion(db *DB, ref string) (Suggestion, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Suggestion{}, ErrNoSuggestion{Ref: ref}
	}
	if s, err := GetSuggestion(db, ref); err == nil {
		return s, nil
	}

	rows, err := db.Query("SELECT "+suggestionColumns+" FROM suggestions WHERE uuid LIKE ? ORDER BY created_on", ref+"%")
	if err != nil {
		return Suggestion{}, errors.Wrap(err, "querying suggestions by prefix")
	}
	defer rows.Close()

	var matches []Suggestion
	for rows.Next() {
		s, err := scanSuggestion(rows)
		if err != nil {
			return Suggestion{}, errors.Wrap(err, "scanning suggestion")
		}
		matches = append(matches, s)
	}
	if err := rows.Err(); err != nil {
		return Suggestion{}, errors.Wrap(err, "reading suggestions by prefix")
	}

	switch len(matches) {
	case 0:
		return Suggestion{}, ErrNoSuggestion{Ref: ref}
	case 1:
		return matches[0], nil
	}
	// a pending match is what a reviewer means; only fall back to the rest
	var pending []Suggestion
	for _, s := range matches {
		if s.Pending() {
			pending = append(pending, s)
		}
	}
	if len(pending) == 1 {
		return pending[0], nil
	}
	return Suggestion{}, ErrAmbiguousSuggestion{Ref: ref, Matches: matches}
}

// TargetDrifted reports whether the node an edit suggestion targets changed
// since the suggestion was made — the reviewer is looking at a stale proposal.
func TargetDrifted(db *DB, s Suggestion) (bool, error) {
	if s.Kind != SuggestEdit {
		return false, nil
	}
	n, err := GetNode(db, s.TargetUUID)
	if err != nil {
		return false, err
	}
	if s.Proposes(FieldName) && n.Name != s.BaseName {
		return true, nil
	}
	if s.Proposes(FieldNote) && n.Note != s.BaseNote {
		return true, nil
	}
	return false, nil
}

// settle marks a suggestion resolved. It refuses to re-settle one that was
// already approved or rejected, so an approval never applies twice.
func settle(db *DB, s Suggestion, status, resultUUID string) error {
	res, err := db.Exec(`UPDATE suggestions SET status = ?, resolved_on = ?, result_uuid = ?
		WHERE uuid = ? AND status = ?`,
		status, time.Now().UnixNano(), resultUUID, s.UUID, SuggestPending)
	if err != nil {
		return errors.Wrapf(err, "settling suggestion %s", s.UUID)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return errors.Errorf("suggestion %s was already reviewed", ShortID(s.UUID))
	}
	return nil
}

// RejectSuggestion settles a suggestion without touching the outline.
func RejectSuggestion(db *DB, s Suggestion) error {
	return settle(db, s, SuggestRejected, "")
}

// ApplySuggestion approves a suggestion and writes it to the outline in one
// transaction: either the node change and the settled status both land, or
// neither does. The updated suggestion comes back so callers can report the
// node an add created.
func ApplySuggestion(db *DB, s Suggestion) (Suggestion, error) {
	if !s.Pending() {
		return s, errors.Errorf("suggestion %s is already %s", ShortID(s.UUID), s.Status)
	}

	tx, err := db.Begin()
	if err != nil {
		return s, errors.Wrap(err, "beginning suggestion tx")
	}
	defer tx.Rollback()

	var resultUUID string
	switch s.Kind {
	case SuggestAdd:
		resultUUID, err = applyAdd(tx, s)
	case SuggestEdit:
		err = applyEdit(tx, s)
	case SuggestComplete, SuggestUncomplete:
		err = applyCompletion(tx, s)
	default:
		err = errors.Errorf("unknown suggestion kind %q", s.Kind)
	}
	if err != nil {
		return s, err
	}
	if err := settle(tx, s, SuggestApproved, resultUUID); err != nil {
		return s, err
	}
	if err := tx.Commit(); err != nil {
		return s, errors.Wrap(err, "committing suggestion")
	}

	s.Status = SuggestApproved
	s.ResolvedOn = time.Now().UnixNano()
	s.ResultUUID = resultUUID
	return s, nil
}

// applyAdd creates the proposed node under its parent, honoring the parent's
// priority unless the suggestion pinned a position.
func applyAdd(tx *DB, s Suggestion) (string, error) {
	parentUUID := s.TargetUUID
	if parentUUID == "" {
		parentUUID = RootUUID
	}
	parent, err := GetNode(tx, parentUUID)
	if err != nil {
		return "", errors.Wrapf(err, "loading parent %s", ShortID(parentUUID))
	}
	if parent.Deleted {
		return "", errors.Errorf("parent %q was deleted", parent.Name)
	}
	if parent.LockValue().Has(LockIndentOutdent) {
		return "", errors.New("parent structure is locked")
	}

	var rank int
	switch s.Position {
	case PositionTop:
		rank, err = FirstRank(tx, parentUUID)
	case PositionBottom:
		rank, err = NextRank(tx, parentUUID)
	default:
		rank, err = PlaceRank(tx, parentUUID)
	}
	if err != nil {
		return "", err
	}

	name := s.Name
	if !s.Raw {
		// chips are created only now: a rejected suggestion leaves none behind
		name, err = ChipifyName(tx, s.Name)
		if err != nil {
			return "", errors.Wrap(err, "creating chips")
		}
	}

	uuid, err := utils.GenerateUUID()
	if err != nil {
		return "", errors.Wrap(err, "generating uuid")
	}
	typ := s.Type
	if typ == "" {
		typ = TypeBullets
	}
	now := time.Now().UnixNano()
	n := Node{
		UUID:       uuid,
		ParentUUID: parentUUID,
		Rank:       rank,
		Name:       name,
		Note:       s.Note,
		Type:       typ,
		Priority:   PriorityUp,
		AddedOn:    now,
		EditedOn:   now,
	}
	if err := n.Insert(tx); err != nil {
		return "", err
	}
	return uuid, nil
}

// applyCompletion writes the proposed done state onto the target node.
func applyCompletion(tx *DB, s Suggestion) error {
	n, err := GetNode(tx, s.TargetUUID)
	if err != nil {
		return errors.Wrapf(err, "loading node %s", ShortID(s.TargetUUID))
	}
	if n.Deleted {
		return errors.Errorf("node %q was deleted", n.Name)
	}
	if n.LockValue().Has(LockReadWrite) {
		return errors.New("node is locked")
	}
	completedAt := int64(0)
	if s.Kind == SuggestComplete {
		completedAt = time.Now().Unix()
	}
	_, err = tx.Exec("UPDATE nodes SET completed_at = ?, edited_on = ? WHERE uuid = ?",
		completedAt, time.Now().UnixNano(), s.TargetUUID)
	return errors.Wrap(err, "updating completion state")
}

// applyEdit writes the proposed fields onto the target node.
func applyEdit(tx *DB, s Suggestion) error {
	n, err := GetNode(tx, s.TargetUUID)
	if err != nil {
		return errors.Wrapf(err, "loading node %s", ShortID(s.TargetUUID))
	}
	if n.Deleted {
		return errors.Errorf("node %q was deleted", n.Name)
	}
	if n.LockValue().Has(LockReadWrite) {
		return errors.New("node is locked")
	}

	sets := []string{"edited_on = ?"}
	vals := []interface{}{time.Now().UnixNano()}

	if s.Proposes(FieldName) {
		name := s.Name
		if !s.Raw {
			name, err = ChipifyName(tx, s.Name)
			if err != nil {
				return errors.Wrap(err, "creating chips")
			}
		}
		sets = append(sets, "name = ?")
		vals = append(vals, name)
	}
	if s.Proposes(FieldNote) {
		sets = append(sets, "note = ?")
		vals = append(vals, s.Note)
	}
	if s.Proposes(FieldType) {
		if !ValidTypes[s.Type] {
			return errors.Errorf("unknown type %q: %s", s.Type, TypeList())
		}
		sets = append(sets, "type = ?")
		vals = append(vals, s.Type)
	}
	if len(sets) == 1 {
		return errors.New("suggestion proposes no fields")
	}

	vals = append(vals, s.TargetUUID)
	if _, err := tx.Exec("UPDATE nodes SET "+strings.Join(sets, ", ")+" WHERE uuid = ?", vals...); err != nil {
		return errors.Wrap(err, "updating node")
	}
	return nil
}

// ShortID renders the 6-character prefix the CLI prints for nodes and
// suggestions alike.
func ShortID(uuid string) string {
	if len(uuid) > 6 {
		return uuid[:6]
	}
	return uuid
}

// NormalizeFields orders and dedupes a proposed-field list so the stored value
// is canonical regardless of flag order.
func NormalizeFields(fields []string) string {
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
