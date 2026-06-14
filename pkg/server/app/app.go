package app

import (
	"github.com/lflow/lflow/pkg/server/mailer"
	"github.com/lflow/lflow/pkg/shared/clock"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

var (
	// ErrEmptyDB is an error for missing database connection in the app configuration
	ErrEmptyDB = errors.New("No database connection was provided")
	// ErrEmptyClock is an error for missing clock in the app configuration
	ErrEmptyClock = errors.New("No clock was provided")
	// ErrEmptyBaseURL is an error for missing BaseURL content in the app configuration
	ErrEmptyBaseURL = errors.New("No BaseURL was provided")
	// ErrEmptyEmailBackend is an error for missing EmailBackend content in the app configuration
	ErrEmptyEmailBackend = errors.New("No EmailBackend was provided")
	// ErrEmptyHTTP500Page is an error for missing HTTP 500 page content
	ErrEmptyHTTP500Page = errors.New("No HTTP 500 error page was set")
)

// App is an application context
type App struct {
	DB                  *gorm.DB
	Clock               clock.Clock
	EmailBackend        mailer.Backend
	Files               map[string][]byte
	HTTP500Page         []byte
	BaseURL             string
	DisableRegistration bool
	Port                string
	DBPath              string
	AssetBaseURL        string
}

// Validate validates the app configuration
func (a *App) Validate() error {
	if a.BaseURL == "" {
		return ErrEmptyBaseURL
	}
	if a.Clock == nil {
		return ErrEmptyClock
	}
	if a.EmailBackend == nil {
		return ErrEmptyEmailBackend
	}
	if a.DB == nil {
		return ErrEmptyDB
	}
	if a.HTTP500Page == nil {
		return ErrEmptyHTTP500Page
	}

	return nil
}
