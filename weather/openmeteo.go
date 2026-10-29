package weather

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wisborg/fitactivity"
)

// DefaultURL is Open-Meteo's archive: ERA5 and its kin from 1940, and
// ECMWF's IFS analysis for the days the reanalysis has not reached yet.
const DefaultURL = "https://archive-api.open-meteo.com/v1/archive"

// hourly are the variables asked for, in the order Conditions holds them.
var hourly = []string{
	"temperature_2m", "dew_point_2m", "relative_humidity_2m",
	"wind_speed_10m", "wind_gusts_10m", "wind_direction_10m",
	"surface_pressure", "shortwave_radiation",
}

// ErrNoPosition is an activity with no fix to ask the weather for.
var ErrNoPosition = errors.New("the activity records no position to ask the weather for")

// Client fetches an activity's weather from Open-Meteo.
type Client struct {
	HTTP *http.Client
	// URL is the archive's address: DefaultURL unless a test says
	// otherwise.
	URL string
	// CacheDir is where answers are kept, or empty to keep none.
	CacheDir string
	// Now is the clock answers are judged fresh by.
	Now func() time.Time
}

// New is a client keeping its answers in cacheDir, which may be empty to
// keep none.
func New(cacheDir string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, URL: DefaultURL, CacheDir: cacheDir, Now: time.Now}
}

// DefaultCacheDir is where answers are kept unless a program says
// otherwise: under $XDG_CACHE_HOME where it is set, and otherwise the
// system's cache directory.
func DefaultCacheDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		var err error
		if base, err = os.UserCacheDir(); err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "fitactivity", "weather"), nil
}

// settled is how long after a day the archive's answer about it is final:
// the reanalysis replaces the analysis five days on, and a day more is for
// the update to land.
const settled = 6 * 24 * time.Hour

// unsettledTTL is how long an answer about days not yet settled is kept
// before it is asked for again.
const unsettledTTL = 6 * time.Hour

// Fetch is the weather t was done in, at the places Places picks along it,
// for the hours it covers and one either side. It asks once, for all the
// places together, and not at all when the cache has the answer.
func (c *Client) Fetch(ctx context.Context, t *fitactivity.Track) (*Report, error) {
	pts := Places(t)
	if len(pts) == 0 {
		return nil, ErrNoPosition
	}
	start, end := t.Samples[0].Time.UTC().Add(-time.Hour), t.Samples[len(t.Samples)-1].Time.UTC().Add(time.Hour)
	q := url.Values{
		"latitude":        {join(pts, func(p [2]float64) float64 { return p[0] })},
		"longitude":       {join(pts, func(p [2]float64) float64 { return p[1] })},
		"start_date":      {start.Format("2006-01-02")},
		"end_date":        {end.Format("2006-01-02")},
		"hourly":          {strings.Join(hourly, ",")},
		"wind_speed_unit": {"ms"},
		"timeformat":      {"unixtime"},
	}
	key := cacheKey(q)
	if data, ok := c.cached(key, end); ok {
		if r, err := decode(data); err == nil {
			r.Cached = true
			return r, nil
		}
	}
	data, err := c.get(ctx, c.URL+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	r, err := decode(data)
	if err != nil {
		return nil, err
	}
	c.keep(key, data)
	return r, nil
}

// placeSpacing is how far apart the places weather is asked for are: about
// the archive's finer grid, so a long run or ride crosses into the next
// cell before it is given another place, and a short one is given one.
const placeSpacing = 10000.0

// maxPlaces bounds how many places one activity is asked for, however far
// it went: a flight would otherwise be hundreds.
const maxPlaces = 20

// Places are where along t its weather is asked for: its first fix, then
// one each placeSpacing metres along it -- or, for an activity that would
// need more than maxPlaces, maxPlaces spread evenly along it, so a 210 km
// ride is asked for at 20 places 11 km apart rather than 11 places 20 km
// apart.
func Places(t *fitactivity.Track) [][2]float64 {
	var total float64
	var last [2]float64
	first := true
	for _, s := range t.Samples {
		if !s.HasGPS {
			continue
		}
		if !first {
			total += greatCircle(last[0], last[1], s.Lat, s.Lon)
		}
		last, first = [2]float64{s.Lat, s.Lon}, false
	}
	// The places are where the distance along t passes each multiple of
	// spacing, so total/(maxPlaces-1) gives maxPlaces, the last at the end;
	// the loop is only for rounding.
	for spacing := math.Max(placeSpacing, total/(maxPlaces-1)*(1-1e-9)); ; spacing *= 1.01 {
		var pts [][2]float64
		var along float64
		next := spacing
		for _, s := range t.Samples {
			if !s.HasGPS {
				continue
			}
			p := [2]float64{s.Lat, s.Lon}
			if len(pts) == 0 {
				pts, last = append(pts, p), p
				continue
			}
			along += greatCircle(last[0], last[1], p[0], p[1])
			last = p
			if along >= next {
				pts, next = append(pts, p), next+spacing
			}
		}
		if len(pts) <= maxPlaces {
			return pts
		}
	}
}

func join(pts [][2]float64, f func([2]float64) float64) string {
	s := make([]string, len(pts))
	for i, p := range pts {
		// Four decimals is about ten metres: far finer than the grid,
		// and no finer than the cache needs to tell runs apart.
		s[i] = strconv.FormatFloat(f(p), 'f', 4, 64)
	}
	return strings.Join(s, ",")
}

// get asks Open-Meteo, saying why where it refuses.
func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asking Open-Meteo for the weather: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("reading Open-Meteo's answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(data, &e) == nil && e.Reason != "" {
			return nil, fmt.Errorf("Open-Meteo answered %s: %s", resp.Status, e.Reason)
		}
		return nil, fmt.Errorf("Open-Meteo answered %s", resp.Status)
	}
	return data, nil
}

// decode reads Open-Meteo's answer: one place's object, or a list of them
// for several. An hour missing any of the variables is left out.
func decode(data []byte) (*Report, error) {
	var many []json.RawMessage
	if err := json.Unmarshal(data, &many); err != nil {
		many = []json.RawMessage{data}
	}
	r := &Report{}
	for _, raw := range many {
		var a struct {
			Latitude  float64                    `json:"latitude"`
			Longitude float64                    `json:"longitude"`
			Elevation float64                    `json:"elevation"`
			Hourly    map[string]json.RawMessage `json:"hourly"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("decoding Open-Meteo's answer: %w", err)
		}
		var times []int64
		if err := json.Unmarshal(a.Hourly["time"], &times); err != nil {
			return nil, fmt.Errorf("decoding Open-Meteo's answer: its hours: %w", err)
		}
		cols := make([][]*float64, len(hourly))
		for i, name := range hourly {
			if err := json.Unmarshal(a.Hourly[name], &cols[i]); err != nil || len(cols[i]) != len(times) {
				return nil, fmt.Errorf("decoding Open-Meteo's answer: no %s for each hour", name)
			}
		}
		p := Place{Lat: a.Latitude, Lon: a.Longitude, Elevation: a.Elevation}
	hours:
		for h, ts := range times {
			var v [8]float64
			for i := range hourly {
				if cols[i][h] == nil {
					continue hours
				}
				v[i] = *cols[i][h]
			}
			p.Hours = append(p.Hours, Conditions{Time: time.Unix(ts, 0).UTC(),
				Temperature: v[0], DewPoint: v[1], Humidity: v[2],
				WindSpeed: v[3], Gusts: v[4], WindFrom: v[5], Pressure: v[6], Radiation: v[7]})
		}
		r.Places = append(r.Places, p)
	}
	if len(r.Places) == 0 {
		return nil, errors.New("decoding Open-Meteo's answer: no places in it")
	}
	return r, nil
}

func cacheKey(q url.Values) string {
	sum := sha256.Sum256([]byte(q.Encode()))
	return hex.EncodeToString(sum[:12])
}

// cached is the answer kept under key, unless it is about days not yet
// settled and older than unsettledTTL.
func (c *Client) cached(key string, end time.Time) ([]byte, bool) {
	if c.CacheDir == "" {
		return nil, false
	}
	p := filepath.Join(c.CacheDir, key+".json")
	info, err := os.Stat(p)
	if err != nil {
		return nil, false
	}
	if final := info.ModTime().Sub(end) > settled; !final && c.Now().Sub(info.ModTime()) > unsettledTTL {
		return nil, false
	}
	data, err := os.ReadFile(p)
	return data, err == nil
}

// keep keeps an answer under key. Failing to is no failure of the fetch:
// the next one asks again.
func (c *Client) keep(key string, data []byte) {
	if c.CacheDir == "" || os.MkdirAll(c.CacheDir, 0o700) != nil {
		return
	}
	f, err := os.CreateTemp(c.CacheDir, ".answer-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	_, werr := f.Write(data)
	if f.Close() != nil || werr != nil {
		return
	}
	os.Rename(f.Name(), filepath.Join(c.CacheDir, key+".json"))
}
