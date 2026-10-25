package strava

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake is a stand-in for Strava: its OAuth and the few API calls the client
// makes, answering for one invented athlete with invented activities near
// 10°N 20°E. It counts what it is asked.
type fake struct {
	t   *testing.T
	srv *httptest.Server

	mu      sync.Mutex
	calls   map[string]int
	access  string // the access token it currently accepts
	refresh string // the refresh token it currently accepts
	expires time.Time
	limited bool
	now     func() time.Time // the fake's clock, which a test moves with the client's
	listed  []Summary
	revoked bool
}

const (
	testClientID = "4242"
	testSecret   = "invented-secret"
	testAthlete  = 77
	testActivity = 1001
)

func newFake(t *testing.T) *fake {
	f := &fake{t: t, calls: map[string]int{}, access: "access-1", refresh: "refresh-1", expires: time.Now().Add(6 * time.Hour), now: time.Now}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", f.token)
	mux.HandleFunc("/oauth/deauthorize", func(w http.ResponseWriter, r *http.Request) {
		f.count("deauthorize")
		f.revoked = true
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/api/v3/", f.api)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) count(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[name]++
}

func (f *fake) called(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

func (f *fake) token(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Form.Get("client_id") != testClientID || r.Form.Get("client_secret") != testSecret {
		http.Error(w, `{"message":"Bad Request"}`, http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		f.calls["code"]++
		if r.Form.Get("code") != "the-code" {
			http.Error(w, `{"message":"Bad Request"}`, http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":%q,"expires_at":%d,"athlete":{"id":%d,"firstname":"Ann","lastname":"Invented"}}`,
			f.access, f.refresh, f.expires.Unix(), testAthlete)
	case "refresh_token":
		f.calls["refresh"]++
		if r.Form.Get("refresh_token") != f.refresh {
			http.Error(w, `{"message":"Bad Request"}`, http.StatusBadRequest)
			return
		}
		f.access, f.refresh = "access-2", "refresh-2"
		f.expires = f.now().Add(6 * time.Hour)
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":%q,"expires_at":%d}`, f.access, f.refresh, f.expires.Unix())
	}
}

func (f *fake) api(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	access, limited := f.access, f.limited
	f.mu.Unlock()
	w.Header().Set("X-RateLimit-Limit", "200,2000")
	w.Header().Set("X-RateLimit-Usage", "5,50")
	w.Header().Set("X-ReadRateLimit-Limit", "100,1000")
	w.Header().Set("X-ReadRateLimit-Usage", "4,40")
	if limited {
		w.Header().Set("X-ReadRateLimit-Usage", "100,140")
		http.Error(w, `{"message":"Rate Limit Exceeded"}`, http.StatusTooManyRequests)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+access {
		http.Error(w, `{"message":"Authorization Error"}`, http.StatusUnauthorized)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v3")
	switch path {
	case fmt.Sprintf("/activities/%d", testActivity):
		f.count("details")
		fmt.Fprintf(w, `{"id":%d,"name":"Invented Run","description":"Words.","sport_type":"Run","start_date":"2026-04-02T06:00:00Z","external_id":"9.fit",
			"segment_efforts":[{"name":"Invented Hill","start_date":"2026-04-02T06:00:01Z","elapsed_time":1,"segment":{"id":5}}]}`, testActivity)
	case fmt.Sprintf("/activities/%d/streams", testActivity):
		f.count("streams")
		if !strings.Contains(r.URL.Query().Get("keys"), "latlng") {
			f.t.Errorf("streams asked for %q", r.URL.Query().Get("keys"))
		}
		fmt.Fprint(w, `[{"type":"time","data":[0,1,2]},{"type":"latlng","data":[[10,20],[10.0001,20],[10.0002,20]]},{"type":"heartrate","data":[120,121,122]}]`)
	case "/athlete/activities":
		f.count("list")
		q := r.URL.Query()
		page := q.Get("page")
		var out []Summary
		if page == "1" {
			for _, s := range f.listed {
				after, _ := time.Parse(time.RFC3339, "1970-01-01T00:00:00Z")
				if a := q.Get("after"); a != "" {
					var sec int64
					fmt.Sscan(a, &sec)
					after = time.Unix(sec, 0)
				}
				if s.Start.After(after) {
					out = append(out, s)
				}
			}
		}
		json.NewEncoder(w).Encode(out)
	default:
		http.NotFound(w, r)
	}
}

// client is a client of f, its token and credentials in a directory of the
// test's own and its cache in another.
func (f *fake) client(t *testing.T) *Client {
	t.Helper()
	dir := t.TempDir()
	cache, err := OpenCache(filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	c := New(Credentials{ClientID: testClientID, ClientSecret: testSecret, Path: filepath.Join(dir, "strava.yaml")}, cache)
	c.API, c.OAuth = f.srv.URL+"/api/v3", f.srv.URL+"/oauth"
	return c
}

// browser is an athlete who allows access: it follows the authorise page's
// redirect back to where it asks, with the code and scope Strava would add.
func browser(scope string) func(string) error {
	return func(page string) error {
		u, err := url.Parse(page)
		if err != nil {
			return err
		}
		q := u.Query()
		back := q.Get("redirect_uri") + "?" + url.Values{"state": {q.Get("state")}, "code": {"the-code"}, "scope": {scope}}.Encode()
		go func() {
			resp, err := http.Get(back)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

func loggedIn(t *testing.T, f *fake) *Client {
	t.Helper()
	c := f.client(t)
	if _, err := c.Login(context.Background(), LoginOptions{Open: browser("read,activity:read_all"), Out: &strings.Builder{}, Wait: 10 * time.Second}); err != nil {
		t.Fatalf("login: %v", err)
	}
	return c
}

func TestLogin_KeepsTheTokenForItsOwnerAlone(t *testing.T) {
	f := newFake(t)
	c := loggedIn(t, f)
	tok, err := LoadToken(c.TokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if tok.AthleteID != testAthlete || tok.AthleteName != "Ann Invented" || tok.RefreshToken != "refresh-1" || tok.ClientID != testClientID {
		t.Errorf("token %+v", tok)
	}
	info, err := os.Stat(c.TokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode %v, want 600", info.Mode().Perm())
	}
}

func TestLogin_ByPaste(t *testing.T) {
	f := newFake(t)
	c := f.client(t)
	var out strings.Builder
	pr, pw := ioPipe()
	w := &syncWriter{b: &out}
	go func() {
		// Read the page the athlete is told to open, and paste back where
		// Strava would have sent them.
		for !strings.Contains(w.String(), "authorize?") {
			time.Sleep(5 * time.Millisecond)
		}
		line := w.String()
		page := line[strings.Index(line, "http"):]
		page = page[:strings.IndexAny(page, " \n")]
		u, _ := url.Parse(page)
		fmt.Fprintf(pw, "http://localhost/exchange_token?state=%s&code=the-code&scope=read,activity:read_all\n", u.Query().Get("state"))
	}()
	if _, err := c.Login(context.Background(), LoginOptions{Out: w, Paste: pr, Wait: 10 * time.Second}); err != nil {
		t.Fatalf("login: %v", err)
	}
	if f.called("code") != 1 {
		t.Errorf("code exchanged %d times", f.called("code"))
	}
}

func TestLogin_WithoutActivitiesIsRefused(t *testing.T) {
	f := newFake(t)
	c := f.client(t)
	_, err := c.Login(context.Background(), LoginOptions{Open: browser("read"), Out: &strings.Builder{}, Wait: 10 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "reading activities") {
		t.Fatalf("err %v", err)
	}
	if _, err := LoadToken(c.TokenPath()); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("a token was kept: %v", err)
	}
}

func TestCheckAnswer_AnotherLoginsStateIsRefused(t *testing.T) {
	err := checkAnswer(url.Values{"state": {"other"}, "code": {"c"}}, "mine")
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Errorf("err %v", err)
	}
	if err := checkAnswer(url.Values{"error": {"access_denied"}}, "mine"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("err %v", err)
	}
}

func TestToken_RefreshedWhenItRunsOut(t *testing.T) {
	f := newFake(t)
	c := loggedIn(t, f)
	later := func() time.Time { return time.Now().Add(7 * time.Hour) }
	c.Now = later
	f.mu.Lock()
	f.now = later
	f.mu.Unlock()
	if _, err := c.Details(context.Background(), testActivity); err != nil {
		t.Fatal(err)
	}
	tok, _ := LoadToken(c.TokenPath())
	if f.called("refresh") != 1 || tok.RefreshToken != "refresh-2" || tok.AccessToken != "access-2" {
		t.Errorf("refreshed %d times; token %+v", f.called("refresh"), tok)
	}
}

func TestToken_AnotherApplicationsLoginIsNotUsed(t *testing.T) {
	f := newFake(t)
	c := loggedIn(t, f)
	other := New(Credentials{ClientID: "9999", ClientSecret: "x", Path: filepath.Join(filepath.Dir(c.TokenPath()), "strava.yaml")}, nil)
	if _, err := other.Token(context.Background()); err == nil || !strings.Contains(err.Error(), "another application") {
		t.Errorf("err %v", err)
	}
}

func TestNotLoggedIn(t *testing.T) {
	f := newFake(t)
	if _, err := f.client(t).Details(context.Background(), testActivity); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("err %v", err)
	}
}

func TestTrack_FromTheStreamsAndKeptADay(t *testing.T) {
	f := newFake(t)
	c := loggedIn(t, f)
	tr, err := c.Track(context.Background(), testActivity)
	if err != nil {
		t.Fatal(err)
	}
	if tr.SourcePath != "strava:1001" || len(tr.Samples) != 3 || tr.Title != "Invented Run" || !tr.Samples[2].HasHeartRate {
		t.Errorf("track %s %q, %d samples", tr.SourcePath, tr.Title, len(tr.Samples))
	}
	if _, err := c.Track(context.Background(), testActivity); err != nil {
		t.Fatal(err)
	}
	if f.called("details") != 1 || f.called("streams") != 1 {
		t.Errorf("asked for details %d and streams %d times; the second is the cache's", f.called("details"), f.called("streams"))
	}
	// A day later the cache has nothing, and opening it deletes the old.
	c.cache.now = func() time.Time { return time.Now().Add(CacheTTL + time.Minute) }
	if err := c.cache.Expire(); err != nil {
		t.Fatal(err)
	}
	if n, _ := c.cache.Entries(testAthlete); n != 0 {
		t.Errorf("%d answers kept past a day", n)
	}
	if _, err := c.Track(context.Background(), testActivity); err != nil {
		t.Fatal(err)
	}
	if f.called("streams") != 2 {
		t.Errorf("streams asked for %d times; want again after a day", f.called("streams"))
	}
}

func TestAttach_TheActivityThatStartedThen(t *testing.T) {
	f := newFake(t)
	start := time.Date(2026, 4, 2, 6, 0, 0, 0, time.UTC)
	f.listed = []Summary{
		{ID: 1, Name: "Someone else's upload a minute off", Start: start.Add(time.Minute)},
		{ID: testActivity, Name: "Invented Run", Start: start.Add(30 * time.Second), ExternalID: "9.fit"},
		{ID: 3, Name: "An hour later", Start: start.Add(time.Hour)},
	}
	c := loggedIn(t, f)
	s, err := c.Match(context.Background(), start, "9.fit")
	if err != nil || s == nil || s.ID != testActivity {
		t.Fatalf("match %+v, %v; want the upload of the same file", s, err)
	}
	s, _ = c.Match(context.Background(), start, "")
	if s == nil || s.ID != testActivity {
		t.Errorf("match %+v; want the nearest start", s)
	}
	if s, _ := c.Match(context.Background(), start.Add(-time.Hour), ""); s != nil {
		t.Errorf("match %+v for a time nothing started near", s)
	}
}

func TestRateLimited(t *testing.T) {
	f := newFake(t)
	c := loggedIn(t, f)
	f.limited = true
	now := time.Date(2026, 4, 2, 6, 7, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }
	_, err := c.Details(context.Background(), testActivity)
	var rl *ErrRateLimited
	if !errors.As(err, &rl) {
		t.Fatalf("err %v", err)
	}
	if rl.Usage.ReadShort != 100 || !rl.Resets.Equal(time.Date(2026, 4, 2, 6, 15, 0, 0, time.UTC)) {
		t.Errorf("usage %+v, resets %v", rl.Usage, rl.Resets)
	}
}

func TestLogout_RevokesAndForgets(t *testing.T) {
	f := newFake(t)
	c := loggedIn(t, f)
	if _, err := c.Details(context.Background(), testActivity); err != nil {
		t.Fatal(err)
	}
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.revoked {
		t.Error("not revoked on Strava")
	}
	if _, err := LoadToken(c.TokenPath()); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("token kept: %v", err)
	}
	if n, _ := c.cache.Entries(testAthlete); n != 0 {
		t.Errorf("%d answers kept after logout", n)
	}
}

func TestLoadCredentials(t *testing.T) {
	t.Setenv(EnvClientID, "")
	t.Setenv(EnvClientSecret, "")
	t.Setenv(EnvConfig, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "strava.yaml")
	if err := os.WriteFile(path, []byte("strava:\n  client_id: 4242\n  client_secret: s3cret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientID != "4242" || c.ClientSecret != "s3cret" || c.Path != path || !strings.Contains(c.Warning, "chmod 600") {
		t.Errorf("credentials %+v", c)
	}
	if TokenPath(c) != filepath.Join(dir, "strava-token.json") {
		t.Errorf("token beside credentials: %s", TokenPath(c))
	}
	t.Setenv(EnvClientSecret, "from-env")
	if c, _ := LoadCredentials(path); c.ClientSecret != "from-env" {
		t.Errorf("environment not over the file: %+v", c)
	}
	t.Setenv(EnvClientSecret, "")
	if _, err := LoadCredentials(filepath.Join(dir, "missing.yaml")); err == nil || errors.Is(err, ErrNoCredentials) {
		t.Errorf("a file named and missing: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := LoadCredentials(""); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("nothing anywhere: %v", err)
	}
}
