package strava

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wisborg/secrets"
)

// Token is what a login leaves: the athlete who allowed the application in,
// what they allowed, and the tokens to act for them with. The access token
// lasts six hours and the refresh token gets the next; Strava may hand out
// a new refresh token with any refresh, and the old one is then not to be
// used again.
type Token struct {
	// ClientID is the application the token is for: a token is of no use
	// with another, and a changed client id means logging in again.
	ClientID string `json:"client_id"`

	AthleteID   int64  `json:"athlete_id"`
	AthleteName string `json:"athlete_name,omitempty"`

	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	// Scope is what the athlete allowed, as Strava lists it:
	// "read,activity:read_all".
	Scope string `json:"scope"`
}

// ErrNotLoggedIn is what a fetch answers before any login, or after a
// logout.
var ErrNotLoggedIn = errors.New("not logged in to Strava")

// tokenFile is the file the token is kept in where the keychain is not
// used, beside the credentials file.
const tokenFile = "strava-token.json"

// TokenPath is where the token for credentials c is kept where the
// keychain is not used, and where its lock is kept either way: beside the
// file the credentials came from, or in ~/.config when they came from
// elsewhere. The token is the program's to write, so it is a file of its
// own rather than a change to the user's.
func TokenPath(c Credentials) string {
	dir := configDir()
	if c.Path != "" {
		dir = filepath.Dir(c.Path)
	}
	return filepath.Join(dir, tokenFile)
}

// tokenStore is where the token is kept: the system's keychain where one
// is used, the file at path where not.
func tokenStore(path string) secrets.Store {
	return secrets.Kept(secrets.File{Path: path, Name: TokenName})
}

// LoadToken reads the token in the file at path. A path with nothing there
// is ErrNotLoggedIn.
func LoadToken(path string) (Token, error) {
	return loadToken(secrets.File{Path: path, Name: TokenName})
}

// SaveToken writes t to the file at path, for its owner alone.
func SaveToken(path string, t Token) error {
	return saveToken(secrets.File{Path: path, Name: TokenName}, t)
}

// RemoveToken deletes the token in the file at path; one that is not there
// is already gone.
func RemoveToken(path string) error {
	return secrets.File{Path: path, Name: TokenName}.Delete(TokenName)
}

// loadToken reads the token in s. None there is ErrNotLoggedIn.
func loadToken(s secrets.Store) (Token, error) {
	data, err := s.Get(TokenName)
	if errors.Is(err, secrets.ErrNotFound) {
		return Token{}, ErrNotLoggedIn
	}
	if err != nil {
		return Token{}, err
	}
	var t Token
	if err := json.Unmarshal([]byte(data), &t); err != nil {
		// json's error can quote what it failed on, which is the token.
		return Token{}, fmt.Errorf("reading the Strava token in %s: it is not a token", s.Where(TokenName))
	}
	if t.RefreshToken == "" {
		return Token{}, fmt.Errorf("reading the Strava token in %s: it holds no refresh token", s.Where(TokenName))
	}
	return t, nil
}

// saveToken keeps t in s.
func saveToken(s secrets.Store, t Token) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return s.Set(TokenName, string(append(data, '\n')))
}

// lockToken holds the lock beside the token at path until the returned
// function is called: two programs refreshing at once would each spend the
// same refresh token, and the second's might already be void.
func lockToken(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		unlockFile(f)
		f.Close()
	}, nil
}
