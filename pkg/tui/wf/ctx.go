package wf

import (
	"path/filepath"

	"github.com/lflow/lflow/pkg/tui/config"
	"github.com/lflow/lflow/pkg/tui/consts"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/pkg/errors"
)

// ClientFromCtx builds a workflowy client from the api key in the config file.
// Set workflowy.apiKey in the config — there is no login command.
func ClientFromCtx(ctx context.DnoteCtx) (Client, error) {
	cf, err := config.Read(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "reading config")
	}
	if cf.Workflowy.APIKey == "" {
		return nil, errors.Errorf("no workflowy api key · set workflowy.apiKey in %s", config.GetPath(ctx))
	}
	return NewClient(cf.Workflowy.BaseURL, cf.Workflowy.APIKey), nil
}

// JournalFromCtx returns the journal for overwritten local values.
func JournalFromCtx(ctx context.DnoteCtx) Journal {
	return Journal{Path: filepath.Join(ctx.Paths.Data, consts.LflowDirName, "wf-journal.log")}
}
