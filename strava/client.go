package strava

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client acts for the athlete a Token is for, with the application
// Credentials name.
type Client struct {
	creds     Credentials
	tokenPath string
	cache     *Cache

	// HTTP is the client requests are made with.
	HTTP *http.Client
	// API and OAuth are where Strava's API and its OAuth endpoints are:
	// Strava's own unless a test says otherwise.
	API, OAuth string
	// Now is the clock tokens are judged by.
	Now func() time.Time

	// Usage is the rate-limit usage Strava reported with the last answer,
	// when HasUsage.
	Usage    RateUsage
	HasUsage bool
}

// The addresses of Strava's API and of its OAuth endpoints.
const (
	DefaultAPI   = "https://www.strava.com/api/v3"
	DefaultOAuth = "https://www.strava.com/oauth"
)

// New is a client for the application c, its token kept at TokenPath(c) and
// its answers in cache, which may be nil to keep none.
func New(c Credentials, cache *Cache) *Client {
	return &Client{creds: c, tokenPath: TokenPath(c), cache: cache,
		HTTP: &http.Client{Timeout: 60 * time.Second},
		API:  DefaultAPI, OAuth: DefaultOAuth, Now: time.Now}
}

// TokenPath is where the client keeps its token.
func (c *Client) TokenPath() string { return c.tokenPath }

// Cache is where the client keeps its answers, or nil.
func (c *Client) Cache() *Cache { return c.cache }

// Scopes are what a login asks the athlete to allow: their profile, which
// says whose the token is, and their activities, the private ones too, all
// to read and nothing to change.
const Scopes = "read,activity:read_all"

// AuthURL is the page on Strava where the athlete allows the application
// in, sending them back to redirect with state, which the caller checks
// comes back unchanged.
func (c *Client) AuthURL(redirect, state string) string {
	q := url.Values{
		"client_id":       {c.creds.ClientID},
		"redirect_uri":    {redirect},
		"response_type":   {"code"},
		"approval_prompt": {"auto"},
		"scope":           {Scopes},
		"state":           {state},
	}
	return c.OAuth + "/authorize?" + q.Encode()
}

// tokenAnswer is the OAuth token endpoint's answer, to a code and to a
// refresh alike; only a code's names the athlete.
type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	Athlete      *struct {
		ID        int64  `json:"id"`
		FirstName string `json:"firstname"`
		LastName  string `json:"lastname"`
	} `json:"athlete"`
}

// Exchange trades the code a login sent back, with the scope the athlete
// allowed, for a token, and keeps it. A login that did not allow reading
// activities is refused here, with what to do, rather than failing at the
// first fetch.
func (c *Client) Exchange(ctx context.Context, code, scope string) (Token, error) {
	if !strings.Contains(scope, "activity:read") {
		return Token{}, fmt.Errorf("the login allowed %q, which does not include reading activities; log in again and leave \"View data about your activities\" ticked", scope)
	}
	var a tokenAnswer
	if err := c.post(ctx, c.OAuth+"/token", url.Values{
		"client_id": {c.creds.ClientID}, "client_secret": {c.creds.ClientSecret},
		"code": {code}, "grant_type": {"authorization_code"},
	}, &a); err != nil {
		return Token{}, fmt.Errorf("exchanging the login's code for a token: %w", err)
	}
	t := Token{ClientID: c.creds.ClientID, AccessToken: a.AccessToken, RefreshToken: a.RefreshToken,
		ExpiresAt: time.Unix(a.ExpiresAt, 0).UTC(), Scope: scope}
	if a.Athlete != nil {
		t.AthleteID = a.Athlete.ID
		t.AthleteName = strings.TrimSpace(a.Athlete.FirstName + " " + a.Athlete.LastName)
	}
	if t.AccessToken == "" || t.RefreshToken == "" {
		return Token{}, errors.New("exchanging the login's code for a token: Strava's answer holds no token")
	}
	unlock, err := lockToken(c.tokenPath)
	if err != nil {
		return Token{}, err
	}
	defer unlock()
	return t, SaveToken(c.tokenPath, t)
}

// refreshMargin is how long before its expiry an access token is replaced:
// long enough that one is not used as it runs out.
const refreshMargin = 2 * time.Minute

// Token is the athlete's token, refreshed first if it has run out or is
// about to. The refresh happens under the token's lock, after reading it
// again, so a program that waited on another's refresh uses that one's.
func (c *Client) Token(ctx context.Context) (Token, error) {
	t, err := c.loadToken()
	if err != nil || c.Now().Add(refreshMargin).Before(t.ExpiresAt) {
		return t, err
	}
	unlock, err := lockToken(c.tokenPath)
	if err != nil {
		return Token{}, err
	}
	defer unlock()
	if t, err = c.loadToken(); err != nil || c.Now().Add(refreshMargin).Before(t.ExpiresAt) {
		return t, err
	}
	var a tokenAnswer
	if err := c.post(ctx, c.OAuth+"/token", url.Values{
		"client_id": {c.creds.ClientID}, "client_secret": {c.creds.ClientSecret},
		"refresh_token": {t.RefreshToken}, "grant_type": {"refresh_token"},
	}, &a); err != nil {
		var he *HTTPError
		if errors.As(err, &he) && (he.Status == http.StatusBadRequest || he.Status == http.StatusUnauthorized) {
			return Token{}, fmt.Errorf("Strava no longer accepts the login (%v); log in again", err)
		}
		return Token{}, fmt.Errorf("refreshing the Strava token: %w", err)
	}
	t.AccessToken, t.ExpiresAt = a.AccessToken, time.Unix(a.ExpiresAt, 0).UTC()
	if a.RefreshToken != "" {
		t.RefreshToken = a.RefreshToken
	}
	return t, SaveToken(c.tokenPath, t)
}

// loadToken reads the token and checks it is this application's.
func (c *Client) loadToken() (Token, error) {
	t, err := LoadToken(c.tokenPath)
	if err != nil {
		return t, err
	}
	if t.ClientID != c.creds.ClientID {
		return Token{}, fmt.Errorf("the Strava login at %s is for another application (client id %s, not %s); log in again", c.tokenPath, t.ClientID, c.creds.ClientID)
	}
	return t, nil
}

// Logout revokes the application's access to the athlete on Strava, then
// forgets the token and everything cached for them. Revoking is done first
// and its failure reported, but the token and cache go whether or not it
// succeeded: a user logging out wants them gone.
func (c *Client) Logout(ctx context.Context) error {
	t, err := c.loadToken()
	if errors.Is(err, ErrNotLoggedIn) {
		return err
	}
	var revoke error
	if err == nil {
		if t, err = c.Token(ctx); err == nil {
			revoke = c.post(ctx, c.OAuth+"/deauthorize", url.Values{"access_token": {t.AccessToken}}, nil)
		} else {
			revoke = err
		}
		if c.cache != nil && t.AthleteID != 0 {
			if err := c.cache.Forget(t.AthleteID); err != nil {
				return err
			}
		}
	}
	if err := RemoveToken(c.tokenPath); err != nil {
		return err
	}
	if revoke != nil {
		return fmt.Errorf("forgot the login, but could not revoke it on Strava (%v); remove the application at https://www.strava.com/settings/apps", revoke)
	}
	return nil
}

// HTTPError is an answer from Strava other than success.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("Strava answered %d %s", e.Status, http.StatusText(e.Status))
	}
	return fmt.Sprintf("Strava answered %d %s: %s", e.Status, http.StatusText(e.Status), e.Message)
}

// RateUsage is how much of its rate limits the application has used, as
// Strava reports with every answer: requests in the current fifteen
// minutes and today, of all kinds and of reads.
type RateUsage struct {
	Short, Daily, ShortLimit, DailyLimit                 int
	ReadShort, ReadDaily, ReadShortLimit, ReadDailyLimit int
}

// ErrRateLimited is a request Strava refused for the application having
// used its rate limit; Resets is when the window that was full starts
// again.
type ErrRateLimited struct {
	Usage  RateUsage
	Resets time.Time
}

func (e *ErrRateLimited) Error() string {
	return fmt.Sprintf("Strava's rate limit for this application is used up (%d of %d reads in fifteen minutes, %d of %d today); it resets at %s",
		e.Usage.ReadShort, e.Usage.ReadShortLimit, e.Usage.ReadDaily, e.Usage.ReadDailyLimit, e.Resets.Local().Format("15:04"))
}

// get asks the API for path with query as the athlete, decoding the answer
// into out, or keeping it whole when out is a *[]byte.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	t, err := c.Token(ctx)
	if err != nil {
		return err
	}
	u := c.API + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	return c.do(req, out)
}

// post sends form to u, decoding the answer into out unless it is nil.
func (c *Client) post(ctx context.Context, u string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if u, ok := usage(resp.Header); ok {
		c.Usage, c.HasUsage = u, true
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return &ErrRateLimited{Usage: c.Usage, Resets: resets(c.Now(), c.Usage)}
	case resp.StatusCode == http.StatusUnauthorized && req.Method == http.MethodGet:
		return fmt.Errorf("%w; log in again", &HTTPError{Status: resp.StatusCode, Message: message(body)})
	case resp.StatusCode/100 != 2:
		return &HTTPError{Status: resp.StatusCode, Message: message(body)}
	}
	if out == nil {
		return nil
	}
	if raw, ok := out.(*[]byte); ok {
		*raw = body
		return nil
	}
	return json.Unmarshal(body, out)
}

// message is what Strava said went wrong, from its error answer.
func message(body []byte) string {
	var e struct {
		Message string `json:"message"`
		Errors  []struct {
			Field, Code string
		} `json:"errors"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	for _, x := range e.Errors {
		e.Message += fmt.Sprintf(" (%s %s)", x.Field, x.Code)
	}
	return e.Message
}

// usage reads the rate-limit headers: "200,2000" limits and "12,345" usage,
// fifteen minutes then the day, overall and for reads.
func usage(h http.Header) (RateUsage, bool) {
	pair := func(name string) (int, int, bool) {
		parts := strings.Split(h.Get(name), ",")
		if len(parts) != 2 {
			return 0, 0, false
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		b, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		return a, b, err1 == nil && err2 == nil
	}
	var u RateUsage
	var ok1, ok2, ok3, ok4 bool
	u.ShortLimit, u.DailyLimit, ok1 = pair("X-RateLimit-Limit")
	u.Short, u.Daily, ok2 = pair("X-RateLimit-Usage")
	u.ReadShortLimit, u.ReadDailyLimit, ok3 = pair("X-ReadRateLimit-Limit")
	u.ReadShort, u.ReadDaily, ok4 = pair("X-ReadRateLimit-Usage")
	if !ok3 || !ok4 {
		u.ReadShortLimit, u.ReadDailyLimit, u.ReadShort, u.ReadDaily = u.ShortLimit, u.DailyLimit, u.Short, u.Daily
	}
	return u, ok1 && ok2
}

// resets is when the full window starts again: the next quarter hour for the
// fifteen-minute limit, the next midnight UTC for the daily one.
func resets(now time.Time, u RateUsage) time.Time {
	now = now.UTC()
	if u.ReadDailyLimit > 0 && u.ReadDaily >= u.ReadDailyLimit || u.DailyLimit > 0 && u.Daily >= u.DailyLimit {
		return now.Truncate(24 * time.Hour).Add(24 * time.Hour)
	}
	return now.Truncate(15 * time.Minute).Add(15 * time.Minute)
}
