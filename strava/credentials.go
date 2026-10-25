// Package strava fetches a user's own activities from Strava's API, for the
// programs built on fitactivity to read as they read a file.
//
// Each user registers their own API application with Strava and gives this
// package its client id and secret; nothing is built in. The secret cannot
// be shipped in a program anybody can read, and an application's rate
// limits and athlete capacity are its own, so one shared between users
// would be everybody's quota and Strava's review to pass. See Credentials.
//
// What Strava's API Policy asks of a client is built in rather than left to
// the caller:
//
//   - Strava data is kept no longer than CacheTTL -- a day -- and is gone
//     from the cache once older, well inside the seven days the Policy
//     allows and the 48 hours in which a deletion on Strava must show.
//   - Only the logged-in athlete's own activities are fetched.
//   - A rate limit reached is reported, with when it resets, and never
//     retried in a loop.
//
// The Policy also forbids using Strava data in operating an AI application,
// including putting it into one's context; a program that passes what this
// package fetches to a language model is outside it.
package strava

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Credentials are a Strava API application's: the client id and secret
// shown on the user's own application's settings page,
// https://www.strava.com/settings/api.
type Credentials struct {
	ClientID, ClientSecret string
	// Path is the file they were read from, or empty when both came from
	// the environment.
	Path string
	// Warning is a problem with the file worth telling the user that does
	// not stop it being used: that others can read it.
	Warning string
}

// The environment a user can give credentials in, over or instead of a file.
const (
	EnvConfig       = "STRAVA_CONFIG"
	EnvClientID     = "STRAVA_CLIENT_ID"
	EnvClientSecret = "STRAVA_CLIENT_SECRET"
)

// ErrNoCredentials is what LoadCredentials answers when no client id and
// secret were given anywhere: a user who has not registered an application
// with Strava, or has not said where its details are.
var ErrNoCredentials = errors.New("no Strava API application is set up")

// ConfigPath is the credentials file to read: path when given, then
// $STRAVA_CONFIG, then strava.yaml in $XDG_CONFIG_HOME, then in ~/.config.
//
// ~/.config rather than the system's own configuration directory, which on
// macOS is ~/Library/Application Support: a file a user writes by hand
// belongs where they keep the others, and where every platform's
// command-line tools look.
func ConfigPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	if p := os.Getenv(EnvConfig); p != "" {
		return p, nil
	}
	return filepath.Join(configDir(), "strava.yaml"), nil
}

func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config"
	}
	return filepath.Join(home, ".config")
}

// credentialsFile is the file's shape:
//
//	strava:
//	  client_id: 12345
//	  client_secret: 0123abcd...
type credentialsFile struct {
	Strava struct {
		ClientID     yaml.Node `yaml:"client_id"`
		ClientSecret string    `yaml:"client_secret"`
	} `yaml:"strava"`
}

// LoadCredentials reads the application's client id and secret from the
// file ConfigPath(path) names, with $STRAVA_CLIENT_ID and
// $STRAVA_CLIENT_SECRET over it. A file that is not there is no error when
// the environment gives both; one that is there but unreadable or
// malformed always is.
func LoadCredentials(path string) (Credentials, error) {
	file, err := ConfigPath(path)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	data, err := os.ReadFile(file)
	switch {
	case err == nil:
		var f credentialsFile
		if err := yaml.Unmarshal(data, &f); err != nil {
			return Credentials{}, fmt.Errorf("reading %s: %w", file, err)
		}
		c.ClientID = strings.TrimSpace(f.Strava.ClientID.Value)
		c.ClientSecret = strings.TrimSpace(f.Strava.ClientSecret)
		c.Path = file
		if info, err := os.Stat(file); err == nil && info.Mode().Perm()&0o077 != 0 {
			c.Warning = fmt.Sprintf("%s can be read by others (mode %v); it holds a secret, so chmod 600 it", file, info.Mode().Perm())
		}
	case errors.Is(err, os.ErrNotExist) && path == "" && os.Getenv(EnvConfig) == "":
	default:
		return Credentials{}, fmt.Errorf("reading %s: %w", file, err)
	}
	if v := os.Getenv(EnvClientID); v != "" {
		c.ClientID = v
	}
	if v := os.Getenv(EnvClientSecret); v != "" {
		c.ClientSecret = v
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return Credentials{}, fmt.Errorf("%w: register one at https://www.strava.com/settings/api with \"localhost\" as its Authorization Callback Domain, "+
			"then put its client id and secret in %s as strava.client_id and strava.client_secret, or in $%s and $%s",
			ErrNoCredentials, file, EnvClientID, EnvClientSecret)
	}
	return c, nil
}
