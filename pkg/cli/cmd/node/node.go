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

// Package node groups the node-manipulation commands: add, append, move,
// remove and edit.
package node

import (
	"github.com/lflow/lflow/pkg/cli/cmd/add"
	"github.com/lflow/lflow/pkg/cli/cmd/mv"
	"github.com/lflow/lflow/pkg/cli/cmd/remove"
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/spf13/cobra"
)

// NewCmd returns the node command group.
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Create, move, edit and delete nodes",
	}

	cmd.AddCommand(add.NewCmd(ctx))
	cmd.AddCommand(add.NewAppendCmd(ctx))
	cmd.AddCommand(mv.NewCmd(ctx))
	cmd.AddCommand(remove.NewCmd(ctx))
	cmd.AddCommand(newEditCmd(ctx))

	return cmd
}
