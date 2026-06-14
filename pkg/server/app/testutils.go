package app

import (
	"github.com/lflow/lflow/pkg/clock"
	"github.com/lflow/lflow/pkg/server/assets"
	"github.com/lflow/lflow/pkg/server/testutils"
)

// NewTest returns an app for a testing environment
func NewTest() App {
	return App{
		Clock:               clock.NewMock(),
		EmailBackend:        &testutils.MockEmailbackendImplementation{},
		HTTP500Page:         assets.MustGetHTTP500ErrorPage(),
		BaseURL:             "http://127.0.0.0.1",
		Port:                "3000",
		DisableRegistration: false,
		DBPath:              "",
		AssetBaseURL:        "",
	}
}
