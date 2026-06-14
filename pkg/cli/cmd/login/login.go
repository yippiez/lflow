package login

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/lflow/lflow/pkg/cli/client"
	"github.com/lflow/lflow/pkg/cli/consts"
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/lflow/lflow/pkg/cli/infra"
	"github.com/lflow/lflow/pkg/cli/log"
	"github.com/lflow/lflow/pkg/cli/ui"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var usernameFlag, passwordFlag, apiEndpointFlag string

// NewCmd returns a new login command
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to the lflow server",
		RunE:  newRun(ctx),
	}

	f := cmd.Flags()
	f.StringVarP(&usernameFlag, "username", "u", "", "email address for authentication")
	f.StringVarP(&passwordFlag, "password", "p", "", "password for authentication")
	f.StringVar(&apiEndpointFlag, "apiEndpoint", "", "API endpoint to connect to, defaults to the config value")

	return cmd
}

// Do dervies credentials on the client side and requests a session token from the server
func Do(ctx context.DnoteCtx, email, password string) error {
	signinResp, err := client.Signin(ctx, email, password)
	if err != nil {
		return errors.Wrap(err, "requesting session")
	}

	db := ctx.DB
	tx, err := db.Begin()
	if err != nil {
		return errors.Wrap(err, "beginning a transaction")
	}

	if err := database.UpsertSystem(tx, consts.SystemSessionKey, signinResp.Key); err != nil {
		return errors.Wrap(err, "saving session key")
	}
	if err := database.UpsertSystem(tx, consts.SystemSessionKeyExpiry, strconv.FormatInt(signinResp.ExpiresAt, 10)); err != nil {
		return errors.Wrap(err, "saving session key")
	}

	tx.Commit()

	return nil
}

func getUsername() (string, error) {
	if usernameFlag != "" {
		return usernameFlag, nil
	}

	var email string
	if err := ui.PromptInput("email", &email); err != nil {
		return "", errors.Wrap(err, "getting email input")
	}
	if email == "" {
		return "", errors.New("Email is empty")
	}

	return email, nil
}

func getPassword() (string, error) {
	if passwordFlag != "" {
		return passwordFlag, nil
	}

	var password string
	if err := ui.PromptPassword("password", &password); err != nil {
		return "", errors.Wrap(err, "getting password input")
	}
	if password == "" {
		return "", errors.New("Password is empty")
	}

	return password, nil
}

func getBaseURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", errors.Wrap(err, "parsing url")
	}

	if u.Scheme == "" || u.Host == "" {
		return "", nil
	}

	return fmt.Sprintf("%s://%s", u.Scheme, u.Host), nil
}

func getServerDisplayURL(ctx context.DnoteCtx) string {
	baseURL, err := getBaseURL(ctx.APIEndpoint)
	if err != nil {
		return ""
	}

	return baseURL
}

func getGreeting(ctx context.DnoteCtx) string {
	base := "Welcome to Lflow"

	serverURL := getServerDisplayURL(ctx)
	if serverURL == "" {
		return fmt.Sprintf("%s\n", base)
	}

	return fmt.Sprintf("%s (%s)\n", base, serverURL)
}

func newRun(ctx context.DnoteCtx) infra.RunEFunc {
	return func(cmd *cobra.Command, args []string) error {
		// Override APIEndpoint if flag was provided
		if apiEndpointFlag != "" {
			ctx.APIEndpoint = apiEndpointFlag
		}

		greeting := getGreeting(ctx)
		log.Plain(greeting)

		email, err := getUsername()
		if err != nil {
			return errors.Wrap(err, "getting email input")
		}

		password, err := getPassword()
		if err != nil {
			return errors.Wrap(err, "getting password input")
		}
		if password == "" {
			return errors.New("Password is empty")
		}

		log.Debug("Logging in with email: %s and password: (length %d)\n", email, len(password))

		err = Do(ctx, email, password)
		if errors.Cause(err) == client.ErrInvalidLogin {
			log.Error("wrong login\n")
			return nil
		} else if err != nil {
			return errors.Wrap(err, "logging in")
		}

		log.Success("logged in\n")

		return nil
	}

}
