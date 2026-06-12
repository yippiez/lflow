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

package operations

import (
	"github.com/lflow/lflow/pkg/server/database"
	"github.com/lflow/lflow/pkg/server/helpers"
	"github.com/lflow/lflow/pkg/server/permissions"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// GetNode retrieves a node for the given user
func GetNode(db *gorm.DB, uuid string, user *database.User) (database.Node, bool, error) {
	zeroNode := database.Node{}
	if !helpers.ValidateUUID(uuid) {
		return zeroNode, false, nil
	}

	var node database.Node
	err := db.Where("nodes.uuid = ? AND deleted = ?", uuid, false).Preload("User").Find(&node).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return zeroNode, false, nil
	} else if err != nil {
		return zeroNode, false, errors.Wrap(err, "finding node")
	}

	if ok := permissions.ViewNode(user, node); !ok {
		return zeroNode, false, nil
	}

	return node, true, nil
}
