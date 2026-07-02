package auth

import (
	stdcontext "context"
	"time"

	"github.com/lflow/lflow/pkg/runtime"
	"github.com/lflow/lflow/pkg/tui/colab"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/utils/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// loginTimeout bounds the interactive browser sign-in.
const loginTimeout = 5 * time.Minute

func newColabCmd(ctx context.DnoteCtx) *cobra.Command {
	return &cobra.Command{
		Use:   "colab",
		Short: "Authenticate with Google Colab for compute runtimes",
		Long: "Opens a browser to sign in with Google and authorize Colab access. " +
			"The resulting token is stored in the local lflow database and used to " +
			"start compute runtime sessions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runColab(ctx)
		},
	}
}

func runColab(ctx context.DnoteCtx) error {
	c, cancel := stdcontext.WithTimeout(stdcontext.Background(), loginTimeout)
	defer cancel()

	log.Info("Opening your browser to sign in with Google for Colab access...\n")

	tok, err := runtime.Login(c)
	if err != nil {
		return errors.Wrap(err, "google sign-in")
	}

	if err := colab.NewStore(ctx.DB).Save(c, tok); err != nil {
		return errors.Wrap(err, "saving colab credentials")
	}

	log.Success("Colab authentication complete. Credentials saved to the local database.\n")
	return nil
}
