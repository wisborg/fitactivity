// Package strava fetches a user's own activities from Strava's API, for the
// programs built on fitactivity to read as they read a file.
//
// Each user registers their own API application with Strava and gives this
// package its client id and secret -- in the environment, the system's
// keychain or a file; see Secrets -- and nothing is built in. The secret cannot
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

	"github.com/wisborg/secrets"
)

// Credentials are a Strava API application's: the client id and secret
// shown on the user's own application's settings page,
// https://www.strava.com/settings/api.
type Credentials struct {
	ClientID, ClientSecret string
	// Path is the file they were read from, or empty when neither came
	// from one.
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

// The secrets the client uses, by the names package secrets knows them by:
// in the keychain, the items client_id, client_secret and token of the
// service wisborg/strava.
const (
	ClientIDName     = "strava/client_id"
	ClientSecretName = "strava/client_secret"
	TokenName        = "strava/token"
)

// Secrets are the secrets the client uses, each with where it is looked
// for: the application's client id and secret in the environment, then the
// system's keychain, then the file ConfigPath(path) names --
//
//	strava:
//	  client_id: 12345
//	  client_secret: 0123abcd...
//
// -- and the login's token where package secrets keeps what a program is
// handed itself: the keychain where there is one, strava-token.json beside
// that file where not.
func Secrets(path string) ([]secrets.Secret, error) {
	file, err := ConfigPath(path)
	if err != nil {
		return nil, err
	}
	creds := secrets.MappingFile{Path: file,
		Keys: map[string][]string{ClientIDName: {"strava.client_id"}, ClientSecretName: {"strava.client_secret"}},
		// The usual file need not be there when the environment or the
		// keychain has both; one named, by path or $STRAVA_CONFIG, must.
		Optional: path == "" && os.Getenv(EnvConfig) == ""}
	env := secrets.Env{ClientIDName: EnvClientID, ClientSecretName: EnvClientSecret}
	chain := secrets.Chain{env, secrets.Keychain{}, creds}
	return []secrets.Secret{
		{Name: ClientIDName, About: "client id of your Strava API application", Sources: chain},
		{Name: ClientSecretName, About: "client secret of your Strava API application", Sources: chain},
		{Name: TokenName, About: "your Strava login, kept by \"strava login\"", Kept: true,
			Sources: secrets.Chain{tokenStore(filepath.Join(filepath.Dir(file), tokenFile))}},
	}, nil
}

// LoadCredentials is the application's client id and secret, from wherever
// Secrets(path) says they are looked for. A file that is there but is not
// a mapping is an error even when the environment gives both.
func LoadCredentials(path string) (Credentials, error) {
	list, err := Secrets(path)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	missing := false
	for _, s := range list[:2] {
		v, _, err := s.Get()
		if errors.Is(err, secrets.ErrNotFound) {
			missing = true
			continue
		}
		if err != nil {
			return Credentials{}, err
		}
		if s.Name == ClientIDName {
			c.ClientID = v
		} else {
			c.ClientSecret = v
		}
	}
	// The token is kept beside the file, there whether or not it gave
	// these, as Secrets says it is.
	file, _ := ConfigPath(path)
	if _, err := os.Stat(file); err == nil {
		f := secrets.MappingFile{Path: file}
		c.Path, c.Warning = file, f.Warning()
	}
	if missing {
		return Credentials{}, fmt.Errorf("%w: register one at https://www.strava.com/settings/api with \"localhost\" as its Authorization Callback Domain, "+
			"then put its client id and secret in %s as strava.client_id and strava.client_secret, in $%s and $%s, or in %s as %s and %s",
			ErrNoCredentials, file, EnvClientID, EnvClientSecret, secrets.KeychainName(), ClientIDName, ClientSecretName)
	}
	return c, nil
}
