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

// Package editor implements the inline (scrollback-mode) outline editor.
package editor

import (
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/pkg/errors"
)

// Run opens the inline node editor on the given node.
func Run(ctx context.DnoteCtx, nodeUUID string) error {
	return errors.New("the inline editor is not built yet (phase 3); use --print meanwhile")
}
