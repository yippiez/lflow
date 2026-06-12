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

package main

import (
	"os"
	"strings"

	"github.com/lflow/lflow/pkg/cli/infra"
	"github.com/lflow/lflow/pkg/cli/log"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"

	// commands
	"github.com/lflow/lflow/pkg/cli/cmd/export"
	"github.com/lflow/lflow/pkg/cli/cmd/node"
	"github.com/lflow/lflow/pkg/cli/cmd/root"
	"github.com/lflow/lflow/pkg/cli/cmd/server"
	"github.com/lflow/lflow/pkg/cli/cmd/version"
	wfcmd "github.com/lflow/lflow/pkg/cli/cmd/wf"
)

// apiEndpoint and versionTag are populated during link time
var apiEndpoint string
var versionTag = "master"

// parseDBPath extracts --dbPath flag value from command line arguments
// regardless of where it appears (before or after subcommand).
// Returns empty string if not found.
func parseDBPath(args []string) string {
	for i, arg := range args {
		// Handle --dbPath=value
		if strings.HasPrefix(arg, "--dbPath=") {
			return strings.TrimPrefix(arg, "--dbPath=")
		}
		// Handle --dbPath value
		if arg == "--dbPath" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func main() {
	// Parse flags early to get --dbPath before initializing database
	// We need to manually parse --dbPath because it can appear after the subcommand
	// (e.g., "lflow sync --full --dbPath=./custom.db") and root.ParseFlags only
	// parses flags before the subcommand.
	dbPath := parseDBPath(os.Args[1:])

	// Initialize context - defaultAPIEndpoint is used when creating new config file
	ctx, err := infra.Init(versionTag, apiEndpoint, dbPath)
	if err != nil {
		panic(errors.Wrap(err, "initializing context"))
	}
	defer ctx.DB.Close()

	root.Register(node.NewCmd(*ctx))
	root.Register(export.NewCmd(*ctx))
	root.Register(server.NewCmd(*ctx))
	root.Register(wfcmd.NewCmd(*ctx))
	root.Register(version.NewCmd(*ctx))

	if err := root.Execute(); err != nil {
		log.Errorf("%s\n", err.Error())
		os.Exit(1)
	}
}
