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

package root

import (
	"github.com/spf13/cobra"
)

var root = &cobra.Command{
	Use:           "lflow",
	Short:         "local-first terminal outline tool",
	SilenceErrors: true,
	SilenceUsage:  true,
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

func init() {
	// --help is the only help surface: no help subcommand
	root.SetHelpCommand(&cobra.Command{Use: "no-help", Hidden: true})
	root.SetHelpFunc(renderHelpFunc)
	root.SetUsageFunc(renderUsageFunc)
}

// GetRoot returns the root command
func GetRoot() *cobra.Command {
	return root
}

// Register adds a new command
func Register(cmd *cobra.Command) {
	root.AddCommand(cmd)
}

// Execute runs the main command
func Execute() error {
	return root.Execute()
}
