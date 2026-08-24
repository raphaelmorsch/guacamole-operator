package branding

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ExtensionNamespace = "guacamole-operator-branding"
	LogoResourcePath   = "images/logo.png"
	LogoMIME           = "image/png"
	JARFileName        = "operator-branding.jar"
)

// Options describes the login branding extension to generate.
type Options struct {
	Title   string
	HasLogo bool
}

type manifest struct {
	GuacamoleVersion string            `json:"guacamoleVersion"`
	Name             string            `json:"name"`
	Namespace        string            `json:"namespace"`
	CSS              []string          `json:"css,omitempty"`
	Resources        map[string]string `json:"resources,omitempty"`
	Translations     []string          `json:"translations,omitempty"`
}

type translations struct {
	APP struct {
		Name string `json:"NAME"`
	} `json:"APP"`
}

// Manifest returns guac-manifest.json for the branding extension.
func Manifest(opts Options) ([]byte, error) {
	m := manifest{
		GuacamoleVersion: "*",
		Name:             "Guacamole Operator Login Branding",
		Namespace:        ExtensionNamespace,
		CSS:              []string{"css/branding.css"},
	}
	if opts.HasLogo {
		m.Resources = map[string]string{
			LogoResourcePath: LogoMIME,
		}
	}
	if strings.TrimSpace(opts.Title) != "" {
		m.Translations = []string{"translations/en.json"}
	}
	return json.MarshalIndent(m, "", "    ")
}

// CSS returns the login page override stylesheet.
func CSS(hasLogo bool) []byte {
	if !hasLogo {
		return []byte("/* Guacamole Operator login branding (title only) */\n")
	}
	return []byte(fmt.Sprintf(`.login-ui .login-dialog .logo {
    width: 9em;
    height: auto;
    background-size: contain;
    background-repeat: no-repeat;
    background-image: url('app/ext/%s/%s');
}
`, ExtensionNamespace, LogoResourcePath))
}

// Translations returns en.json overriding APP.NAME when title is non-empty.
func Translations(title string) ([]byte, error) {
	t := translations{}
	t.APP.Name = title
	return json.MarshalIndent(t, "", "    ")
}
