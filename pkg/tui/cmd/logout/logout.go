package logout

import (
	"database/sql"

	"github.com/lflow/lflow/pkg/tui/client"
	"github.com/lflow/lflow/pkg/tui/consts"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/infra"
	"github.com/lflow/lflow/pkg/tui/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// ErrNotLoggedIn is an error for logging out when not logged in
var ErrNotLoggedIn = errors.New("not logged in")

var apiEndpointFlag string

// NewCmd returns a new logout command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Log out from the lflow server",
		RunE:  newRun(ctx),
	}

	f := cmd.Flags()
	f.StringVar(&apiEndpointFlag, "apiEndpoint", "", "API endpoint to connect to, defaults to the config value")

	return cmd
}

// Do performs logout
func Do(ctx context.DnoteCtx) error {
	db := ctx.DB
	tx, err := db.Begin()
	if err != nil {
		return errors.Wrap(err, "beginning a transaction")
	}

	var key string
	err = database.GetSystem(tx, consts.SystemSessionKey, &key)
	if errors.Cause(err) == sql.ErrNoRows {
		return ErrNotLoggedIn
	} else if err != nil {
		return errors.Wrap(err, "getting session key")
	}

	err = client.Signout(ctx, key)
	if err != nil {
		return errors.Wrap(err, "requesting logout")
	}

	if err := database.DeleteSystem(tx, consts.SystemSessionKey); err != nil {
		return errors.Wrap(err, "deleting session key")
	}
	if err := database.DeleteSystem(tx, consts.SystemSessionKeyExpiry); err != nil {
		return errors.Wrap(err, "deleting session key expiry")
	}

	tx.Commit()

	return nil
}

func newRun(ctx context.DnoteCtx) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		// Override APIEndpoint if flag was provided
		if apiEndpointFlag != "" {
			ctx.APIEndpoint = apiEndpointFlag
		}

		err := Do(ctx)
		if err == ErrNotLoggedIn {
			log.Error("not logged in\n")
			return nil
		} else if err != nil {
			return errors.Wrap(err, "logging out")
		}

		log.Success("logged out\n")

		return nil
	}
}
