package strava

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
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

// TokenPath is where the token for credentials c is kept: beside the file
// the credentials came from, or in ~/.config when they came from the
// environment. The token is the program's to write, so it is a file of its
// own rather than a change to the user's.
func TokenPath(c Credentials) string {
	dir := configDir()
	if c.Path != "" {
		dir = filepath.Dir(c.Path)
	}
	return filepath.Join(dir, "strava-token.json")
}

// LoadToken reads the token at path. A path with nothing there is
// ErrNotLoggedIn.
func LoadToken(path string) (Token, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Token{}, ErrNotLoggedIn
	}
	if err != nil {
		return Token{}, err
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return Token{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if t.RefreshToken == "" {
		return Token{}, fmt.Errorf("reading %s: it holds no refresh token", path)
	}
	return t, nil
}

// SaveToken writes t to path for its owner alone, whole or not at all: to a
// file beside it first, renamed over it, so a crash part-way leaves the
// token it had rather than half of the new one.
func SaveToken(path string, t Token) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".strava-token-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// RemoveToken deletes the token at path; one that is not there is already
// gone.
func RemoveToken(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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
