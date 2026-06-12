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

// Package server groups the lflow-server commands: login, logout and sync.
package server

import (
	"github.com/lflow/lflow/pkg/cli/cmd/login"
	"github.com/lflow/lflow/pkg/cli/cmd/logout"
	"github.com/lflow/lflow/pkg/cli/cmd/sync"
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/spf13/cobra"
)

// NewCmd returns the server command group.
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Log in to and sync with a self-hosted lflow server",
	}

	cmd.AddCommand(login.NewCmd(ctx))
	cmd.AddCommand(logout.NewCmd(ctx))
	cmd.AddCommand(sync.NewCmd(ctx))

	return cmd
}
