package strava

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wisborg/fitactivity"
)

// Summary is an activity as Strava lists it: enough to choose one by.
type Summary struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	SportType  string    `json:"sport_type"`
	Start      time.Time `json:"start_date"`
	Distance   float64   `json:"distance"`
	MovingTime int       `json:"moving_time"`
	// ExternalID is the name of the file the device uploaded, when Strava
	// kept it.
	ExternalID string `json:"external_id"`
}

// Activities lists the athlete's activities that started in [after,
// before), oldest first, a page at a time. A zero after or before leaves
// that end open.
func (c *Client) Activities(ctx context.Context, after, before time.Time) ([]Summary, error) {
	const perPage = 200
	var out []Summary
	for page := 1; ; page++ {
		q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}
		if !after.IsZero() {
			q.Set("after", strconv.FormatInt(after.Unix()-1, 10))
		}
		if !before.IsZero() {
			q.Set("before", strconv.FormatInt(before.Unix(), 10))
		}
		var got []Summary
		if err := c.get(ctx, "/athlete/activities", q, &got); err != nil {
			return nil, fmt.Errorf("listing Strava activities: %w", err)
		}
		for _, s := range got {
			if (after.IsZero() || !s.Start.Before(after)) && (before.IsZero() || s.Start.Before(before)) {
				out = append(out, s)
			}
		}
		if len(got) < perPage {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// MatchWindow is how far apart a recording's start and a Strava activity's
// may be for them to be the same activity: a watch and Strava agree to the
// second, and this allows for a recording trimmed or a clock adrift.
const MatchWindow = 2 * time.Minute

// Match is the athlete's Strava activity that started nearest start, within
// MatchWindow, preferring one uploaded from a file named file when several
// are that near; nil when there is none.
func (c *Client) Match(ctx context.Context, start time.Time, file string) (*Summary, error) {
	got, err := c.Activities(ctx, start.Add(-MatchWindow), start.Add(MatchWindow+time.Second))
	if err != nil {
		return nil, err
	}
	// The same file's upload first, then the nearest start.
	rank := func(s Summary) (bool, time.Duration) {
		return file == "" || s.ExternalID != file, absDuration(s.Start.Sub(start))
	}
	var best *Summary
	for i := range got {
		other, off := rank(got[i])
		if best == nil {
			best = &got[i]
			continue
		}
		bestOther, bestOff := rank(*best)
		if !other && bestOther || other == bestOther && off < bestOff {
			best = &got[i]
		}
	}
	return best, nil
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// Details is Strava's JSON for activity id, without its streams: what it
// says about the activity beyond the recording. From the cache when it has
// it, and kept there.
func (c *Client) Details(ctx context.Context, id int64) ([]byte, error) {
	t, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	if data, ok := c.cache.get(t.AthleteID, strconv.FormatInt(id, 10), "details"); ok {
		return data, nil
	}
	var data []byte
	if err := c.get(ctx, "/activities/"+strconv.FormatInt(id, 10), nil, &data); err != nil {
		return nil, fmt.Errorf("fetching Strava activity %d: %w", id, err)
	}
	if err := c.cache.put(t.AthleteID, strconv.FormatInt(id, 10), "details", data); err != nil {
		return nil, err
	}
	return data, nil
}

// streamTypes are the streams asked for: what was recorded, which a Track
// is made of. Strava's workings -- smoothed speed and grade, moving -- are
// not asked for.
const streamTypes = "time,latlng,altitude,distance,heartrate,cadence,watts,temp"

// WithStreams is Strava's JSON for activity id with its streams beside it,
// in the shape fitactivity.ReadStravaJSON reads: the activity's details
// with a "streams" list. From the cache when it has it, and kept there.
func (c *Client) WithStreams(ctx context.Context, id int64) ([]byte, error) {
	t, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	if data, ok := c.cache.get(t.AthleteID, strconv.FormatInt(id, 10), "streams"); ok {
		return data, nil
	}
	details, err := c.Details(ctx, id)
	if err != nil {
		return nil, err
	}
	var streams json.RawMessage
	q := url.Values{"keys": {streamTypes}, "key_by_type": {"false"}}
	if err := c.get(ctx, "/activities/"+strconv.FormatInt(id, 10)+"/streams", q, (*[]byte)(&streams)); err != nil {
		return nil, fmt.Errorf("fetching Strava activity %d's streams: %w", id, err)
	}
	data, err := withStreams(details, streams)
	if err != nil {
		return nil, err
	}
	if err := c.cache.put(t.AthleteID, strconv.FormatInt(id, 10), "streams", data); err != nil {
		return nil, err
	}
	return data, nil
}

// withStreams puts streams into details as its "streams".
func withStreams(details []byte, streams json.RawMessage) ([]byte, error) {
	var a map[string]json.RawMessage
	if err := json.Unmarshal(details, &a); err != nil {
		return nil, fmt.Errorf("decoding Strava activity: %w", err)
	}
	a["streams"] = json.RawMessage(bytes.TrimSpace(streams))
	return json.Marshal(a)
}

// Source is how a Track fetched from Strava names where it came from.
func Source(id int64) string { return "strava:" + strconv.FormatInt(id, 10) }

// Track is activity id fetched with its streams, as a fitactivity Track.
func (c *Client) Track(ctx context.Context, id int64) (*fitactivity.Track, error) {
	data, err := c.WithStreams(ctx, id)
	if err != nil {
		return nil, err
	}
	return fitactivity.ReadStravaJSON(data, Source(id))
}

// Attach gives t what Strava says about it -- its title, description and
// segments -- from the athlete's activity that started when t did, and
// reports which that was. None started then is no error, and t is left as
// it was: a recording Strava never had.
func (c *Client) Attach(ctx context.Context, t *fitactivity.Track, file string) (*Summary, error) {
	first, _ := t.Coverage()
	if start := t.Timing.Start; !start.IsZero() {
		first = start
	}
	if first.IsZero() {
		return nil, nil
	}
	s, err := c.Match(ctx, first, file)
	if err != nil || s == nil {
		return nil, err
	}
	data, err := c.Details(ctx, s.ID)
	if err != nil {
		return nil, err
	}
	d, err := fitactivity.ParseStrava(data)
	if err != nil {
		return nil, err
	}
	t.AttachStrava(d)
	return s, nil
}

// Gear is the athlete's piece of gear id in full -- its brand, model and
// description beside the name and distance an activity's details give.
// From the cache when it has it, and kept there.
func (c *Client) Gear(ctx context.Context, id string) (fitactivity.StravaGear, error) {
	t, err := c.Token(ctx)
	if err != nil {
		return fitactivity.StravaGear{}, err
	}
	data, ok := c.cache.get(t.AthleteID, id, "gear")
	if !ok {
		if err := c.get(ctx, "/gear/"+url.PathEscape(id), nil, &data); err != nil {
			return fitactivity.StravaGear{}, fmt.Errorf("fetching Strava gear %s: %w", id, err)
		}
		if err := c.cache.put(t.AthleteID, id, "gear", data); err != nil {
			return fitactivity.StravaGear{}, err
		}
	}
	var g struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Distance    float64 `json:"distance"`
		Brand       string  `json:"brand_name"`
		Model       string  `json:"model_name"`
		Description string  `json:"description"`
		Retired     bool    `json:"retired"`
	}
	if err := json.Unmarshal(data, &g); err != nil {
		return fitactivity.StravaGear{}, fmt.Errorf("decoding Strava gear %s: %w", id, err)
	}
	return fitactivity.StravaGear{ID: g.ID, Name: strings.TrimSpace(g.Name), Distance: g.Distance,
		Brand: strings.TrimSpace(g.Brand), Model: strings.TrimSpace(g.Model),
		Description: strings.TrimSpace(g.Description), Retired: g.Retired}, nil
}

// AddGear gives t's gear on Strava its brand, model and description, when
// t has gear there.
func (c *Client) AddGear(ctx context.Context, t *fitactivity.Track) error {
	if t.Strava == nil || t.Strava.Gear == nil {
		return nil
	}
	g, err := c.Gear(ctx, t.Strava.Gear.ID)
	if err != nil {
		return err
	}
	t.Strava.Gear = &g
	return nil
}
