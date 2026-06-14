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
