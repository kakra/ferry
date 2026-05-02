package i18n

import (
	"embed"

	"github.com/invopop/ctxi18n"
)

//go:embed *.yaml
var locales embed.FS

// Init initializes the i18n system.
func Init() error {
	return ctxi18n.LoadWithDefault(locales, "en")
}
