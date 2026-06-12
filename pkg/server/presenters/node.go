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

package presenters

import (
	"time"

	"github.com/lflow/lflow/pkg/server/database"
)

// Node is a result of PresentNode
type Node struct {
	UUID        string    `json:"uuid"`
	ParentUUID  string    `json:"parent_uuid"`
	Rank        int       `json:"rank"`
	Name        string    `json:"name"`
	Note        string    `json:"note"`
	Layout      string    `json:"layout"`
	MirrorOf    string    `json:"mirror_of"`
	CompletedAt int64     `json:"completed_at"`
	AddedOn     int64     `json:"added_on"`
	EditedOn    int64     `json:"edited_on"`
	USN         int       `json:"usn"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PresentNode presents a node
func PresentNode(node database.Node) Node {
	return Node{
		UUID:        node.UUID,
		ParentUUID:  node.ParentUUID,
		Rank:        node.Rank,
		Name:        node.Name,
		Note:        node.Note,
		Layout:      node.Layout,
		MirrorOf:    node.MirrorOf,
		CompletedAt: node.CompletedAt,
		AddedOn:     node.AddedOn,
		EditedOn:    node.EditedOn,
		USN:         node.USN,
		CreatedAt:   FormatTS(node.CreatedAt),
		UpdatedAt:   FormatTS(node.UpdatedAt),
	}
}

// PresentNodes presents nodes
func PresentNodes(nodes []database.Node) []Node {
	ret := []Node{}

	for _, node := range nodes {
		ret = append(ret, PresentNode(node))
	}

	return ret
}
