package weather

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wisborg/fitactivity"
	"github.com/wisborg/fitactivity/effort"
)

// Every place and value here is invented: a run near 10°N 20°E.

var t0 = time.Date(2026, 4, 2, 6, 0, 0, 0, time.UTC)

// run is a run due north, a sample a second at 3 m/s, for d of time.
func run(d time.Duration) *fitactivity.Track {
	t := &fitactivity.Track{Sport: "running"}
	for i := 0; i <= int(d/time.Second); i++ {
		north := 3.0 * float64(i) // metres
		t.Samples = append(t.Samples, fitactivity.Sample{Time: t0.Add(time.Duration(i) * time.Second),
			HasGPS: true, Lat: 10 + north/111195, Lon: 20})
	}
	return t
}

// place is one place's answer, three hours from 05:00, the wind backing
// from 350° to 10° across 06:00-07:00.
func place(lat, lon float64) string {
	hours := []int64{t0.Add(-time.Hour).Unix(), t0.Unix(), t0.Add(time.Hour).Unix()}
	return fmt.Sprintf(`{"latitude":%g,"longitude":%g,"elevation":400,"hourly":{"time":[%d,%d,%d],
		"temperature_2m":[14,16,18],"dew_point_2m":[9,9,10],"relative_humidity_2m":[70,64,60],
		"wind_speed_10m":[2,4,6],"wind_gusts_10m":[5,7,9],"wind_direction_10m":[340,350,10],
		"surface_pressure":[965,966,967],"shortwave_radiation":[0,100,null]}}`, lat, lon, hours[0], hours[1], hours[2])
}

func fake(t *testing.T, answer string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		q := r.URL.Query()
		if q.Get("wind_speed_unit") != "ms" || q.Get("timeformat") != "unixtime" || q.Get("start_date") != "2026-04-02" {
			t.Errorf("asked %s", r.URL.RawQuery)
		}
		w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func client(srv *httptest.Server, dir string) *Client {
	c := New(dir)
	c.URL = srv.URL
	return c
}

// One place's answer is read, the hour missing a value left out, and kept:
// a second fetch asks nothing.
func TestFetch_ReadAndKept(t *testing.T) {
	srv, n := fake(t, place(10, 20))
	c := client(srv, t.TempDir())
	r, err := c.Fetch(context.Background(), run(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Places) != 1 || len(r.Places[0].Hours) != 2 || r.Places[0].Elevation != 400 || r.Cached {
		t.Fatalf("report %+v", r)
	}
	if r, err = c.Fetch(context.Background(), run(10*time.Minute)); err != nil || !r.Cached || n.Load() != 1 {
		t.Errorf("second fetch: cached %v, %d asked, %v", r.Cached, n.Load(), err)
	}
}

// A long activity is asked for at several places in one request, and each
// sample takes the weather of the nearest.
func TestFetch_SeveralPlaces(t *testing.T) {
	long := run(2 * time.Hour) // 21.6 km
	pts := Places(long)
	if len(pts) != 3 {
		t.Fatalf("%d places for 21.6 km", len(pts))
	}
	srv, _ := fake(t, "["+place(10, 20)+","+strings.Replace(place(10.1, 20), "[14,16,18]", "[4,6,8]", 1)+"]")
	r, err := client(srv, "").Fetch(context.Background(), long)
	if err != nil || len(r.Places) != 2 {
		t.Fatalf("%+v, %v", r, err)
	}
	near, _ := r.At(10.001, 20, true, t0)
	far, _ := r.At(10.099, 20, true, t0)
	if near.Temperature != 16 || far.Temperature != 6 {
		t.Errorf("near %v, far %v", near.Temperature, far.Temperature)
	}
}

// Between hours the weather is interpolated, and the wind turns through
// north rather than the long way round.
func TestAt_Interpolated(t *testing.T) {
	srv, _ := fake(t, place(10, 20))
	r, err := client(srv, "").Fetch(context.Background(), run(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	r.Places[0].Hours = append(r.Places[0].Hours, Conditions{Time: t0.Add(time.Hour), Temperature: 18, WindSpeed: 6, WindFrom: 10})
	c, ok := r.At(10, 20, true, t0.Add(30*time.Minute))
	if !ok || c.Temperature != 17 || c.WindSpeed != 5 || math.Abs(c.WindFrom) > 0.01 && math.Abs(c.WindFrom-360) > 0.01 {
		t.Errorf("conditions %+v", c)
	}
	if _, ok := r.At(10, 20, true, t0.Add(5*time.Hour)); ok {
		t.Error("weather hours past the report")
	}
}

func TestRelativeAndCompass(t *testing.T) {
	// Running north into a northerly: all headwind.
	if h, x := Relative(Conditions{WindSpeed: 4, WindFrom: 0}, 0); math.Abs(h-4) > 1e-9 || math.Abs(x) > 1e-9 {
		t.Errorf("northerly, heading north: %v, %v", h, x)
	}
	// Running east with a northerly: a crosswind from the left.
	if h, x := Relative(Conditions{WindSpeed: 4, WindFrom: 0}, 90); math.Abs(h) > 1e-9 || math.Abs(x+4) > 1e-9 {
		t.Errorf("northerly, heading east: %v, %v", h, x)
	}
	for deg, want := range map[float64]string{0: "N", 359: "N", 270: "W", 200: "SSW", 45: "NE"} {
		if got := Compass(deg); got != want {
			t.Errorf("%v: %s, want %s", deg, got, want)
		}
	}
}

func TestHeadings(t *testing.T) {
	deg, ok := Headings(run(time.Minute))
	for i := range deg {
		if !ok[i] || math.Abs(deg[i]) > 0.01 && math.Abs(deg[i]-360) > 0.01 {
			t.Fatalf("sample %d: %v, %v", i, deg[i], ok[i])
		}
	}
}

func TestFetch_NoPositionAndRefusal(t *testing.T) {
	indoor := &fitactivity.Track{Samples: []fitactivity.Sample{{Time: t0}}}
	if _, err := New("").Fetch(context.Background(), indoor); err != ErrNoPosition {
		t.Errorf("indoor: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":true,"reason":"Parameter 'start_date' is out of range"}`))
	}))
	defer srv.Close()
	if _, err := client(srv, "").Fetch(context.Background(), run(time.Minute)); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Errorf("refused: %v", err)
	}
}

// The summary spans the coolest to the warmest minute, and takes the wind's
// direction from its vector.
func TestSummarise(t *testing.T) {
	srv, _ := fake(t, place(10, 20))
	tr := run(time.Hour)
	r, err := client(srv, "").Fetch(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	r.Places[0].Hours = append(r.Places[0].Hours, Conditions{Time: t0.Add(time.Hour), Temperature: 18, DewPoint: 9, WindSpeed: 4, WindFrom: 350, Gusts: 9})
	s, ok := Summarise(r, tr)
	if !ok || s.TemperatureMin != 16 || s.TemperatureMax != 18 || math.Abs(s.WindSpeed-4) > 1e-9 || math.Abs(s.WindFrom-350) > 0.01 || s.Gusts != 9 {
		t.Errorf("summary %+v, %v", s, ok)
	}
}

// outAndBack is a run north and back south at 3 m/s, half an hour each
// way, in a steady northerly of 5 m/s.
func outAndBack() (*fitactivity.Track, *Report) {
	t := &fitactivity.Track{Sport: "running"}
	lat := 10.0
	for i := 0; i <= 3600; i++ {
		if i > 0 {
			if i <= 1800 {
				lat += 3.0 / 111195
			} else {
				lat -= 3.0 / 111195
			}
		}
		t.Samples = append(t.Samples, fitactivity.Sample{Time: t0.Add(time.Duration(i) * time.Second),
			HasGPS: true, Lat: lat, Lon: 20, HasSpeed: true, Speed: 3})
	}
	var hours []Conditions
	for h := -1; h <= 2; h++ {
		hours = append(hours, Conditions{Time: t0.Add(time.Duration(h) * time.Hour), Temperature: 15, Pressure: 1013.25, WindSpeed: 5, WindFrom: 0})
	}
	return t, &Report{Places: []Place{{Lat: 10, Lon: 20, Hours: hours}}}
}

// The air is the headwind at the runner's height going out and the
// tailwind coming back; over the whole run the wind costs time, since a
// headwind costs more than the same tailwind gives back.
func TestAirAndWindCost(t *testing.T) {
	tr, r := outAndBack()
	air := Air(r, tr, effort.Open)
	if head, density, ok := air(900); !ok || math.Abs(head-3) > 0.01 || math.Abs(density-effort.SeaLevelDensity) > 0.002 {
		t.Errorf("out: %v, %v, %v", head, density, ok)
	}
	if head, _, _ := air(2700); math.Abs(head+3) > 0.01 {
		t.Errorf("back: %v", head)
	}
	flat := func(int) float64 { return 0 }
	// Out into 3 m/s at 3 m/s: still air would have gone about 3.2 m/s for
	// the effort, 21 s/km quicker; back with it, about 2.93 m/s, 8 s/km
	// slower.
	worst, best, ok := WindPace(r, tr, effort.Open, flat)
	if !ok || math.Abs(worst*1000-21) > 3 || math.Abs(best*1000+8) > 2 {
		t.Errorf("pace: worst %.1f s/km, best %.1f s/km, %v", worst*1000, best*1000, ok)
	}
	cost, ok := WindCost(r, tr, effort.Open, flat)
	// By hand: out, the air at 6 m/s past the runner costs 7% more than
	// still air, worth about 110 s over the half hour; back, with none
	// past them, about 40 s is given back.
	if !ok || cost < 50*time.Second || cost > 90*time.Second {
		t.Errorf("cost %v, %v", cost, ok)
	}
}

// A footpod's air power that rises running north says the wind is from the
// north; one that hardly varies says nothing.
func TestFootpodWind(t *testing.T) {
	tr, _ := outAndBack()
	for i := range tr.Samples {
		ap := 1.0
		if i <= 1800 {
			ap = 4
		}
		tr.Samples[i].DevFields = map[string]float64{"Air Power": ap}
	}
	from, strength, ok := FootpodWind(tr)
	if !ok || Apart(from, 0) > 5 || strength < 0.5 {
		t.Errorf("windy: from %v, strength %v, %v", from, strength, ok)
	}
	for i := range tr.Samples {
		tr.Samples[i].DevFields["Air Power"] = 2 + float64(i%2)*0.1
	}
	if _, _, ok := FootpodWind(tr); ok {
		t.Error("a direction from a calm day")
	}
}

// An hour at 25 °C and 50% -- a shade WBGT of 0.7 · 18.0 + 0.3 · 25 =
// 20.1, 5.1 above the best -- costs 2.0% of it; the same run at 1,300 m
// costs about 6% more on top.
func TestHeatAndAltitudeCost(t *testing.T) {
	tr, r := outAndBack()
	for i := range r.Places[0].Hours {
		r.Places[0].Hours[i].Temperature, r.Places[0].Hours[i].Humidity = 25, 50
		r.Places[0].Hours[i].WindSpeed = 0
	}
	cost, lo, hi, ok := HeatCost(r, tr)
	if !ok || math.Abs(lo-20.1) > 0.15 || math.Abs(hi-lo) > 1e-9 || math.Abs(cost.Seconds()-3600*0.004*(lo-15)) > 3 {
		t.Errorf("heat: %v, %v-%v, %v", cost, lo, hi, ok)
	}
	flat := func(int) float64 { return 0 }
	if cost, highest, ok := AltitudeCost(r, tr, effort.Suburban, flat); !ok || cost != 0 || highest != 0 {
		t.Errorf("sea level: %v, %v, %v", cost, highest, ok)
	}
	r.Places[0].Elevation = 1300
	cost, highest, ok := AltitudeCost(r, tr, effort.Suburban, flat)
	if !ok || highest != 1300 || cost < 3*time.Minute || cost > 5*time.Minute {
		t.Errorf("1300 m: %v, %v, %v", cost, highest, ok)
	}
}

// line is a track due north from 10°N, 20°E, a sample each 100 m, over km
// kilometres at 10 m/s.
func line(km int) *fitactivity.Track {
	t := &fitactivity.Track{Sport: "cycling"}
	for i := 0; i <= km*10; i++ {
		t.Samples = append(t.Samples, fitactivity.Sample{Time: t0.Add(time.Duration(i*10) * time.Second),
			HasGPS: true, Lat: 10 + float64(i)*100/111195, Lon: 20})
	}
	return t
}

// Up to 190 km, a place each 10 km; beyond, twenty spread evenly, never
// fewer than a shorter activity was given.
func TestPlaces_Spread(t *testing.T) {
	for km, want := range map[int]int{42: 5, 190: 20, 210: 20, 400: 20, 1000: 20} {
		pts := Places(line(km))
		if len(pts) != want {
			t.Errorf("%d km: %d places, want %d", km, len(pts), want)
			continue
		}
		if km > 190 {
			first, last := pts[0], pts[len(pts)-1]
			if gone := greatCircle(first[0], first[1], last[0], last[1]); gone < float64(km)*1000*0.9 {
				t.Errorf("%d km: the places reach only %.0f m along it", km, gone)
			}
		}
	}
}

// Over a long point-to-point day the summary keeps the start and the end:
// a morning's cool and an afternoon's heat, and a westerly that turned
// easterly, which the averages would call a light northerly.
func TestSummarise_LongDay(t *testing.T) {
	tr := line(200) // 5 h 33 min
	var south, north Place
	south.Lat, south.Lon, north.Lat, north.Lon = 10, 20, 11.8, 20
	for h := -1; h <= 7; h++ {
		f := float64(h) / 6
		c := Conditions{Time: t0.Add(time.Duration(h) * time.Hour), Temperature: 12 + 12*f, WindSpeed: 5, WindFrom: math.Mod(270+180*f, 360)}
		south.Hours, north.Hours = append(south.Hours, c), append(north.Hours, c)
	}
	s, ok := Summarise(&Report{Places: []Place{south, north}}, tr)
	if !ok || s.Start.Temperature != 12 || math.Abs(s.End.Temperature-23.1) > 0.1 ||
		s.Start.WindFrom != 270 || Apart(s.End.WindFrom, 76.5) > 1 || s.WindSteadiness > 0.7 {
		t.Errorf("start %+v, end %+v, steadiness %.2f", s.Start, s.End, s.WindSteadiness)
	}

	steady, _ := outAndBack()
	_, r := outAndBack()
	if s, _ := Summarise(r, steady); math.Abs(s.WindSteadiness-1) > 1e-9 {
		t.Errorf("a steady wind's steadiness %v", s.WindSteadiness)
	}
}
