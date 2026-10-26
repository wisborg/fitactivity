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
