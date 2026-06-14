package wf

import (
	"path/filepath"

	"github.com/lflow/lflow/pkg/cli/config"
	"github.com/lflow/lflow/pkg/cli/consts"
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/pkg/errors"
)

// ClientFromCtx builds a workflowy client from the session id in the config
// file. Set workflowySessionId in the config — there is no login command.
func ClientFromCtx(ctx context.DnoteCtx) (Client, error) {
	cf, err := config.Read(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "reading config")
	}
	if cf.WorkflowySessionID == "" {
		return nil, errors.Errorf("no workflowy session · set workflowySessionId in %s", config.GetPath(ctx))
	}
	return NewInternalClient(cf.WorkflowyBaseURL, cf.WorkflowySessionID), nil
}

// JournalFromCtx returns the journal for overwritten local values.
func JournalFromCtx(ctx context.DnoteCtx) Journal {
	return Journal{Path: filepath.Join(ctx.Paths.Data, consts.LflowDirName, "wf-journal.log")}
}
