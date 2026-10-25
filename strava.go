package fitactivity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// StravaActivity is what Strava says about an activity that its recording
// does not: where it is on Strava, and the segments Strava matched along it.
// Only what concerns the athlete the activity is theirs: nothing here is
// about anybody else, which Strava's API Policy keeps from being shown.
type StravaActivity struct {
	// ID is the activity's on Strava, and URL its page there.
	ID  int64
	URL string
	// Segments are the segments Strava matched, in the order they were
	// started: stretches other athletes drew and named, which cannot be
	// worked out from the recording.
	Segments []StravaSegment
	// PhotoURL is the activity's primary photo, at the largest size
	// Strava gave, or empty.
	PhotoURL string
	// Kudos and Comments are how many the activity has.
	Kudos, Comments int
}

// StravaSegment is one segment effort: the segment's name and when the
// activity entered and left it.
type StravaSegment struct {
	ID         int64
	Name       string
	Start, End time.Time
}

// StravaDetails is what ParseStrava reads from Strava's own description of
// an activity: what a Track gets from AttachStrava, with the start and
// sport to match it to a recording by.
type StravaDetails struct {
	Title, Description string
	// Sport is in the FIT profile's vocabulary, as Track.Sport is, or
	// empty.
	Sport    string
	Start    time.Time
	Activity StravaActivity
	// ExternalID is the name of the file the device uploaded, when Strava
	// kept it: "647051072261.fit".
	ExternalID string
}

// AttachStrava gives t what Strava says about it: its title and
// description, and the activity on Strava. The recording is left as it is.
func (t *Track) AttachStrava(d StravaDetails) {
	t.Title, t.Description = d.Title, d.Description
	a := d.Activity
	t.Strava = &a
}

// stravaActivity is the part of Strava's activity JSON this package reads:
// GET /activities/{id}, and RunGap's ".strava.json" export of the same with
// its streams beside it.
type stravaActivity struct {
	ID           *int64          `json:"id"`
	Name         string          `json:"name"`
	Description  *string         `json:"description"`
	SportType    string          `json:"sport_type"`
	Type         string          `json:"type"`
	StartDate    string          `json:"start_date"`
	ExternalID   *string         `json:"external_id"`
	DeviceWatts  *bool           `json:"device_watts"`
	KudosCount   int             `json:"kudos_count"`
	CommentCount int             `json:"comment_count"`
	Photos       *stravaPhotos   `json:"photos"`
	Efforts      []stravaEffort  `json:"segment_efforts"`
	Streams      json.RawMessage `json:"streams"`
}

type stravaPhotos struct {
	Primary *struct {
		URLs map[string]string `json:"urls"`
	} `json:"primary"`
}

type stravaEffort struct {
	Name        string `json:"name"`
	StartDate   string `json:"start_date"`
	ElapsedTime int    `json:"elapsed_time"`
	Segment     struct {
		ID int64 `json:"id"`
	} `json:"segment"`
}

// stravaStream is one of an activity's streams: a reading per recorded
// point, all of them the same length.
type stravaStream struct {
	Type string            `json:"type"`
	Data []json.RawMessage `json:"data"`
}

// isStrava reports whether data is Strava's JSON for an activity: an object
// with an id and a start date.
func isStrava(data []byte) bool {
	if !bytes.HasPrefix(bytes.TrimSpace(withoutBOM(data)), []byte("{")) {
		return false
	}
	var probe struct {
		ID        *json.Number `json:"id"`
		StartDate string       `json:"start_date"`
	}
	return json.Unmarshal(withoutBOM(data), &probe) == nil && probe.ID != nil && probe.StartDate != ""
}

// ParseStrava reads Strava's JSON for an activity -- what the API's
// GET /activities/{id} answers -- for what it says beyond the recording.
// Its streams, if it carries them, are not read; see Read.
func ParseStrava(data []byte) (StravaDetails, error) {
	var a stravaActivity
	if err := json.Unmarshal(withoutBOM(data), &a); err != nil {
		return StravaDetails{}, fmt.Errorf("decoding Strava activity: %w", err)
	}
	return a.details()
}

func (a *stravaActivity) details() (StravaDetails, error) {
	if a.ID == nil {
		return StravaDetails{}, errors.New("decoding Strava activity: it has no id")
	}
	start, err := parseTime(a.StartDate)
	if err != nil {
		return StravaDetails{}, fmt.Errorf("decoding Strava activity: start_date %w", err)
	}
	d := StravaDetails{
		Title: strings.TrimSpace(a.Name),
		Sport: stravaSport(a.SportType, a.Type),
		Start: start,
		Activity: StravaActivity{
			ID: *a.ID, URL: "https://www.strava.com/activities/" + strconv.FormatInt(*a.ID, 10),
			Kudos: a.KudosCount, Comments: a.CommentCount,
		},
	}
	if a.Description != nil {
		d.Description = strings.TrimSpace(*a.Description)
	}
	if a.ExternalID != nil {
		d.ExternalID = *a.ExternalID
	}
	if a.Photos != nil && a.Photos.Primary != nil {
		d.Activity.PhotoURL = largest(a.Photos.Primary.URLs)
	}
	for _, e := range a.Efforts {
		at, err := parseTime(e.StartDate)
		if err != nil {
			return StravaDetails{}, fmt.Errorf("decoding Strava activity: segment %q start_date %w", e.Name, err)
		}
		d.Activity.Segments = append(d.Activity.Segments, StravaSegment{
			ID: e.Segment.ID, Name: strings.TrimSpace(e.Name),
			Start: at, End: at.Add(time.Duration(e.ElapsedTime) * time.Second),
		})
	}
	return d, nil
}

// largest is the URL of the largest size a photo is offered at, Strava
// keying each by its width in pixels.
func largest(urls map[string]string) string {
	best, url := -1, ""
	for k, u := range urls {
		if n, err := strconv.Atoi(k); err == nil && n > best {
			best, url = n, u
		}
	}
	return url
}

// stravaSport puts Strava's sport into the FIT profile's vocabulary.
// sport_type is the finer and newer of the two Strava gives; type is read
// where it is missing.
func stravaSport(sportType, typ string) string {
	s := sportType
	if s == "" {
		s = typ
	}
	switch s {
	case "Run", "TrailRun", "VirtualRun":
		return "running"
	case "Ride", "VirtualRide", "MountainBikeRide", "GravelRide", "EBikeRide", "EMountainBikeRide", "Velomobile":
		return "cycling"
	case "Walk":
		return "walking"
	case "Hike":
		return "hiking"
	case "Swim":
		return "swimming"
	case "Rowing", "VirtualRow":
		return "rowing"
	}
	return sportName(s)
}

// ReadStravaJSON reads Strava's JSON for an activity with its streams, as
// Read reads one from a file, naming the Track's source as given: a
// client of the API names it by the activity, "strava:1234567890", rather
// than by wherever it keeps the answer.
func ReadStravaJSON(data []byte, source string) (*Track, error) {
	rec, err := readStrava(data)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", source, err)
	}
	return rec.track(source)
}

// readStrava reads Strava's JSON for an activity with its streams beside it
// -- RunGap's ".strava.json" export is one, and so is what a client of the
// API keeps of an activity -- as a recording: a point per reading of the
// streams, timed from the activity's start.
//
// Only what was recorded is read: position, elevation, distance, heart
// rate, cadence, temperature, and power where Strava says the device
// measured it. Strava's own workings -- velocity_smooth, grade_smooth,
// moving -- are not recordings, and a Track read from Strava has no speed,
// as one from a GPX has none. What a FIT file carries and Strava drops --
// developer fields, running dynamics, timer events -- is simply absent.
func readStrava(data []byte) (*recording, error) {
	var a stravaActivity
	if err := json.Unmarshal(withoutBOM(data), &a); err != nil {
		return nil, fmt.Errorf("decoding Strava activity: %w", err)
	}
	d, err := a.details()
	if err != nil {
		return nil, err
	}
	streams, err := stravaStreams(a.Streams)
	if err != nil {
		return nil, err
	}
	rec := &recording{sport: d.Sport, strava: &d}
	offsets, ok := streams["time"]
	if !ok {
		return nil, errors.New("decoding Strava activity: it has no time stream, so no recording; only its details")
	}
	for name, s := range streams {
		if len(s) != len(offsets) {
			return nil, fmt.Errorf("decoding Strava activity: the %s stream has %d readings and the time stream %d", name, len(s), len(offsets))
		}
	}
	power := a.DeviceWatts == nil || *a.DeviceWatts
	for i := range offsets {
		sec, ok, err := stravaNumber(offsets[i])
		if err != nil || !ok {
			return nil, fmt.Errorf("decoding Strava activity: time reading %d is not a number", i)
		}
		f := fix{timed: true}
		f.Time = d.Start.Add(time.Duration(sec * float64(time.Second)))
		if raw, ok := streams["latlng"]; ok {
			var ll []float64
			if err := json.Unmarshal(raw[i], &ll); err == nil && len(ll) == 2 {
				f.HasGPS, f.Lat, f.Lon = true, ll[0], ll[1]
			}
		}
		read := func(name string, set func(float64)) error {
			raw, ok := streams[name]
			if !ok {
				return nil
			}
			v, ok, err := stravaNumber(raw[i])
			if err != nil {
				return fmt.Errorf("decoding Strava activity: %s reading %d: %w", name, i, err)
			}
			if ok {
				set(v)
			}
			return nil
		}
		for _, r := range []struct {
			name string
			set  func(float64)
		}{
			{"altitude", func(v float64) { f.HasElevation, f.Elevation = true, v }},
			{"distance", func(v float64) { f.HasDistance, f.Distance = true, v }},
			{"heartrate", func(v float64) { f.HasHeartRate, f.HeartRate = true, uint8(v) }},
			{"cadence", func(v float64) { f.HasCadence, f.Cadence = true, uint8(v) }},
			{"temp", func(v float64) { f.HasTemperature, f.Temperature = true, int8(v) }},
		} {
			if err := read(r.name, r.set); err != nil {
				return nil, err
			}
		}
		if power {
			if err := read("watts", func(v float64) { f.HasPower, f.Power = true, uint16(v) }); err != nil {
				return nil, err
			}
		}
		rec.points = append(rec.points, f)
	}
	return rec, nil
}

// stravaStreams reads an activity's streams in either of the shapes the API
// answers in: a list of streams each naming its type, or an object keyed by
// type (key_by_type=true).
func stravaStreams(raw json.RawMessage) (map[string][]json.RawMessage, error) {
	out := map[string][]json.RawMessage{}
	raw = json.RawMessage(bytes.TrimSpace(raw))
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	if raw[0] == '[' {
		var list []stravaStream
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("decoding Strava streams: %w", err)
		}
		for _, s := range list {
			out[s.Type] = s.Data
		}
		return out, nil
	}
	var keyed map[string]stravaStream
	if err := json.Unmarshal(raw, &keyed); err != nil {
		return nil, fmt.Errorf("decoding Strava streams: %w", err)
	}
	for k, s := range keyed {
		out[k] = s.Data
	}
	return out, nil
}

// stravaNumber is one reading of a stream: a number, or null for none.
func stravaNumber(raw json.RawMessage) (float64, bool, error) {
	if string(bytes.TrimSpace(raw)) == "null" {
		return 0, false, nil
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, false, fmt.Errorf("%s is not a number", raw)
	}
	return v, true, nil
}
