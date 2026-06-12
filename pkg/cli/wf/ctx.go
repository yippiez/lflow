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

package wf

import (
	"path/filepath"

	"github.com/lflow/lflow/pkg/cli/consts"
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/pkg/errors"
)

// system table keys for the workflowy session.
const (
	SystemWfSession = "wf_session_id"
	SystemWfBaseURL = "wf_base_url"
)

// ClientFromCtx builds a workflowy client from the stored session.
func ClientFromCtx(ctx context.DnoteCtx) (Client, error) {
	var session, baseURL string
	database.GetSystem(ctx.DB, SystemWfSession, &session)
	database.GetSystem(ctx.DB, SystemWfBaseURL, &baseURL)
	if session == "" {
		return nil, errors.New("not logged in to workflowy (run lflow wf login)")
	}
	return NewInternalClient(baseURL, session), nil
}

// JournalFromCtx returns the journal for overwritten local values.
func JournalFromCtx(ctx context.DnoteCtx) Journal {
	return Journal{Path: filepath.Join(ctx.Paths.Data, consts.LflowDirName, "wf-journal.log")}
}
