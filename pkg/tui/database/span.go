package database

import "github.com/pkg/errors"

// NodeSpan is one painted run of a node's name: [Start, End) in runes, styled
// with the same token vocabulary as nodes.style ("bold,color:red"). The stored
// text never carries markers — spans are a parallel annotation, the painter's
// persistence (see editor: paint.go).
type NodeSpan struct {
	NodeUUID string `json:"node_uuid"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Style    string `json:"style"`
}

// ReplaceNodeSpans swaps a node's whole span set in one transaction.
func ReplaceNodeSpans(db *DB, nodeUUID string, spans []NodeSpan) error {
	tx, err := db.Begin()
	if err != nil {
		return errors.Wrap(err, "beginning span tx")
	}
	if _, err := tx.Exec("DELETE FROM node_spans WHERE node_uuid = ?", nodeUUID); err != nil {
		tx.Rollback()
		return errors.Wrap(err, "clearing node spans")
	}
	for _, sp := range spans {
		if sp.End <= sp.Start || sp.Style == "" {
			continue
		}
		if _, err := tx.Exec("INSERT INTO node_spans (node_uuid, start, end, style) VALUES (?, ?, ?, ?)",
			nodeUUID, sp.Start, sp.End, sp.Style); err != nil {
			tx.Rollback()
			return errors.Wrap(err, "inserting node span")
		}
	}
	return errors.Wrap(tx.Commit(), "committing node spans")
}

// AllNodeSpans loads every painted span, grouped by node.
func AllNodeSpans(db *DB) (map[string][]NodeSpan, error) {
	rows, err := db.Query("SELECT node_uuid, start, end, style FROM node_spans ORDER BY node_uuid, start")
	if err != nil {
		return nil, errors.Wrap(err, "querying node spans")
	}
	defer rows.Close()
	out := map[string][]NodeSpan{}
	for rows.Next() {
		var sp NodeSpan
		if err := rows.Scan(&sp.NodeUUID, &sp.Start, &sp.End, &sp.Style); err != nil {
			return nil, errors.Wrap(err, "scanning node span")
		}
		out[sp.NodeUUID] = append(out[sp.NodeUUID], sp)
	}
	return out, rows.Err()
}

// DeleteNodeSpans drops a node's spans (the node was deleted).
func DeleteNodeSpans(db *DB, nodeUUID string) error {
	_, err := db.Exec("DELETE FROM node_spans WHERE node_uuid = ?", nodeUUID)
	return errors.Wrap(err, "deleting node spans")
}
