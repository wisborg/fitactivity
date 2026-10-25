package fitactivity

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// stravaJSON is an invented activity in the shape of Strava's JSON for one,
// with the streams beside it as RunGap exports them: three points near 10°N
// 20°E, nobody's run. edit changes it before it is written.
func stravaJSON(t *testing.T, edit func(map[string]any)) string {
	t.Helper()
	a := map[string]any{
		"id":                 int64(1234567890),
		"name":               "  Invented Run ",
		"description":        "Line one.\n\nLine two.",
		"sport_type":         "TrailRun",
		"type":               "Run",
		"start_date":         "2026-04-02T06:00:00Z",
		"external_id":        "123.fit",
		"device_watts":       true,
		"kudos_count":        3,
		"comment_count":      1,
		"suffer_score":       8.0,
		"workout_type":       1,
		"commute":            true,
		"trainer":            false,
		"perceived_exertion": nil,
		"gear_id":            "g9",
		"gear":               map[string]any{"id": "g9", "name": "Invented Shoes", "distance": 123456.7},
		"photos": map[string]any{"primary": map[string]any{"urls": map[string]any{
			"100": "https://example.invalid/small.jpg", "600": "https://example.invalid/large.jpg",
		}}},
		"segment_efforts": []any{
			map[string]any{"name": "Invented Hill", "start_date": "2026-04-02T06:00:01Z", "elapsed_time": 1, "segment": map[string]any{"id": 42}},
		},
		"streams": []any{
			map[string]any{"type": "time", "data": []any{0, 1, 3}},
			map[string]any{"type": "latlng", "data": []any{[]float64{10, 20}, []float64{10.0001, 20}, nil}},
			map[string]any{"type": "altitude", "data": []any{5, 6.5, 7}},
			map[string]any{"type": "distance", "data": []any{0, 11.1, 22.2}},
			map[string]any{"type": "heartrate", "data": []any{120, nil, 122}},
			map[string]any{"type": "cadence", "data": []any{80, 81, 82}},
			map[string]any{"type": "watts", "data": []any{200, 210, 220}},
			map[string]any{"type": "temp", "data": []any{18, 18, 19}},
			map[string]any{"type": "velocity_smooth", "data": []any{0, 3, 3}},
			map[string]any{"type": "moving", "data": []any{false, true, true}},
		},
	}
	if edit != nil {
		edit(a)
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadStrava(t *testing.T) {
	tr := readTrack(t, writeCourse(t, "activity.txt", stravaJSON(t, nil)))
	if tr.Sport != "running" || tr.Title != "Invented Run" || tr.Description != "Line one.\n\nLine two." {
		t.Errorf("sport %q, title %q, description %q", tr.Sport, tr.Title, tr.Description)
	}
	if len(tr.Samples) != 3 {
		t.Fatalf("%d samples, want 3", len(tr.Samples))
	}
	s := tr.Samples
	if !s[2].Time.Equal(readBase.Add(3*time.Second)) || !s[0].Time.Equal(readBase) {
		t.Errorf("times %v, %v; want the start and three seconds on", s[0].Time, s[2].Time)
	}
	if !s[1].HasGPS || s[1].Lat != 10.0001 || s[2].HasGPS {
		t.Errorf("positions %+v; a null position is no fix", s)
	}
	if !s[1].HasElevation || s[1].Elevation != 6.5 || !s[2].HasDistance || s[2].Distance != 22.2 {
		t.Errorf("elevation and distance %+v", s[1])
	}
	if !s[0].HasHeartRate || s[1].HasHeartRate || s[0].HeartRate != 120 {
		t.Errorf("heart rates %v %v; a null reading is none", s[0].HasHeartRate, s[1].HasHeartRate)
	}
	if !s[2].HasCadence || s[2].Cadence != 82 || !s[2].HasPower || s[2].Power != 220 || !s[2].HasTemperature || s[2].Temperature != 19 {
		t.Errorf("cadence, power, temperature %+v", s[2])
	}
	for _, x := range s {
		if x.HasSpeed {
			t.Errorf("a speed %v from Strava's smoothing, which is not a recording", x.Speed)
		}
	}
	a := tr.Strava
	if a == nil || a.ID != 1234567890 || a.URL != "https://www.strava.com/activities/1234567890" {
		t.Fatalf("strava %+v", a)
	}
	if a.PhotoURL != "https://example.invalid/large.jpg" || a.Kudos != 3 || a.Comments != 1 {
		t.Errorf("photo %q, kudos %d, comments %d", a.PhotoURL, a.Kudos, a.Comments)
	}
	if !a.HasRelativeEffort || a.RelativeEffort != 8 || a.WorkoutType != "race" || !a.Commute || a.Trainer || a.Manual || a.HasPerceivedExertion {
		t.Errorf("relative effort %v %v, workout %q, commute %v, trainer %v, manual %v, exertion %v",
			a.HasRelativeEffort, a.RelativeEffort, a.WorkoutType, a.Commute, a.Trainer, a.Manual, a.HasPerceivedExertion)
	}
	if a.Gear == nil || a.Gear.ID != "g9" || a.Gear.Name != "Invented Shoes" || a.Gear.Distance != 123456.7 {
		t.Errorf("gear %+v", a.Gear)
	}
	if len(a.Segments) != 1 || a.Segments[0].Name != "Invented Hill" || a.Segments[0].ID != 42 ||
		!a.Segments[0].Start.Equal(readBase.Add(time.Second)) || !a.Segments[0].End.Equal(readBase.Add(2*time.Second)) {
		t.Errorf("segments %+v", a.Segments)
	}
}

func TestReadStrava_StreamsKeyedByType(t *testing.T) {
	keyed := stravaJSON(t, func(a map[string]any) {
		m := map[string]any{}
		for _, s := range a["streams"].([]any) {
			s := s.(map[string]any)
			m[s["type"].(string)] = s
		}
		a["streams"] = m
	})
	listed := readTrack(t, writeCourse(t, "a", stravaJSON(t, nil)))
	tr := readTrack(t, writeCourse(t, "b", keyed))
	if len(tr.Samples) != len(listed.Samples) || !reflect.DeepEqual(tr.Samples, listed.Samples) {
		t.Errorf("keyed streams read as %+v, listed as %+v", tr.Samples, listed.Samples)
	}
}

func TestReadStrava_PowerOnlyWhereTheDeviceMeasuredIt(t *testing.T) {
	tr := readTrack(t, writeCourse(t, "a", stravaJSON(t, func(a map[string]any) { a["device_watts"] = false })))
	for _, s := range tr.Samples {
		if s.HasPower {
			t.Fatalf("power %d from Strava's estimate", s.Power)
		}
	}
}

func TestReadStrava_DetailsAloneAreNoRecording(t *testing.T) {
	data := stravaJSON(t, func(a map[string]any) { delete(a, "streams") })
	_, err := Read(writeCourse(t, "a", data))
	if err == nil || !strings.Contains(err.Error(), "only its details") {
		t.Fatalf("err %v, want one saying there are only details", err)
	}
	d, err := ParseStrava([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Invented Run" || d.ExternalID != "123.fit" || !d.Start.Equal(readBase) || d.Sport != "running" {
		t.Errorf("details %+v", d)
	}
	tr := readTrack(t, writeCourse(t, "c.gpx", gpxRun))
	tr.AttachStrava(d)
	if tr.Title != "Invented Run" || tr.Strava == nil || tr.Strava.ID != 1234567890 {
		t.Errorf("attached %q %+v", tr.Title, tr.Strava)
	}
}

func TestReadStrava_StreamsOfDifferentLengthsAreRefused(t *testing.T) {
	data := stravaJSON(t, func(a map[string]any) {
		a["streams"] = append(a["streams"].([]any), map[string]any{"type": "heartrate", "data": []any{1}})
	})
	if _, err := Read(writeCourse(t, "a", data)); err == nil || !strings.Contains(err.Error(), "heartrate") {
		t.Errorf("err %v, want the short stream named", err)
	}
}

func TestReadStrava_OtherJSONIsNotStrava(t *testing.T) {
	_, err := Read(writeCourse(t, "a.json", `{"name": "not an activity"}`))
	if err == nil || !strings.Contains(err.Error(), "Strava activity's JSON") {
		t.Errorf("err %v", err)
	}
}

func TestMergeKeepsTheFirstPiecesTitle(t *testing.T) {
	first := readTrack(t, writeCourse(t, "a", stravaJSON(t, nil)))
	second := readTrack(t, writeCourse(t, "b", stravaJSON(t, func(a map[string]any) {
		a["id"], a["name"], a["start_date"] = int64(2), "Second", "2026-04-02T07:00:00Z"
	})))
	m, err := Merge(second, first)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Invented Run" || m.Strava.ID != 1234567890 {
		t.Errorf("merged title %q, strava %+v; want the earlier piece's", m.Title, m.Strava)
	}
}

func TestReadStrava_WhatIsNotSetIsAbsent(t *testing.T) {
	tr := readTrack(t, writeCourse(t, "a", stravaJSON(t, func(a map[string]any) {
		a["suffer_score"], a["workout_type"], a["commute"] = nil, 0, false
		a["perceived_exertion"] = 6
		delete(a, "gear")
		a["gear_id"] = "b3"
	})))
	a := tr.Strava
	if a.HasRelativeEffort || a.WorkoutType != "" || a.Commute || !a.HasPerceivedExertion || a.PerceivedExertion != 6 {
		t.Errorf("activity %+v", a)
	}
	if a.Gear == nil || a.Gear.ID != "b3" || a.Gear.Name != "" {
		t.Errorf("gear %+v; want its id alone", a.Gear)
	}
}
