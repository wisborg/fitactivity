package weather

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Every value here is invented.

func TestKindAndDescribe(t *testing.T) {
	for code, want := range map[int]Sky{0: Clear, 1: Clear, 2: PartlyCloudy, 3: Overcast, 48: Fog, 53: Drizzle,
		63: Rain, 81: Rain, 73: Snow, 86: Snow, 95: Thunder, 99: Thunder} {
		if got := Kind(code); got != want {
			t.Errorf("Kind(%d) %v, want %v", code, got, want)
		}
	}
	if Describe(61) != "light rain" || Describe(42) != "overcast" {
		t.Errorf("%q, %q", Describe(61), Describe(42))
	}
}

// The weather an activity is said to have been in is the most significant
// that held for a tenth of it: an hour's rain in a four-hour run, not a
// minute's.
func TestMostSignificant(t *testing.T) {
	if got := mostSignificant(map[int]float64{3: 200, 61: 40}); got != 61 {
		t.Errorf("a long shower: %d", got)
	}
	if got := mostSignificant(map[int]float64{3: 200, 61: 1, 95: 1}); got != 3 {
		t.Errorf("a passing shower: %d", got)
	}
}

// The sky is fetched with the rest and is optional: an answer with it
// gives each hour its code, the minutes between hours take the next
// hour's -- a code is of the hour before it -- and the summary the most
// significant weather, what fell and whether it was day.
func TestSky(t *testing.T) {
	answer := strings.Replace(place(10, 20), `"shortwave_radiation":[0,100,null]`,
		`"shortwave_radiation":[0,100,200],"weather_code":[3,3,63],"cloud_cover":[90,100,100],"precipitation":[0,0,2.4],"is_day":[0,1,1]`, 1)
	srv, _ := fake(t, answer)
	tr := run(time.Hour)
	r, err := client(srv, "").Fetch(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := r.At(10, 20, true, t0.Add(30*time.Minute))
	if !ok || !c.HasSky || c.WeatherCode != 63 || c.Precipitation != 2.4 || c.CloudCover != 100 || !c.IsDay {
		t.Errorf("at 06:30 %+v", c)
	}
	s, ok := Summarise(r, tr)
	if !ok || !s.HasSky || s.WeatherCode != 63 || s.Precipitation < 2.3 || s.Precipitation > 2.5 || !s.Day {
		t.Errorf("summary %+v", s)
	}

	srv, _ = fake(t, place(10, 20))
	r, _ = client(srv, "").Fetch(context.Background(), tr)
	if s, _ := Summarise(r, tr); s.HasSky {
		t.Errorf("a sky from an answer without one: %+v", s)
	}
}
