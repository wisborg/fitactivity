package fitactivity

import (
	"math"
	"testing"
	"time"
)

func TestFillDistanceFromGPS(t *testing.T) {
	tr := readTrack(t, writeCourse(t, "flight.csv", fr24Flight))
	if !tr.FillDistanceFromGPS() {
		t.Fatal("no distance filled")
	}
	if d := tr.Samples[0].Distance; !tr.Samples[0].HasDistance || d != 0 {
		t.Errorf("first fix at %v m, want 0", d)
	}
	if tr.Samples[3].HasDistance {
		t.Error("a sample without a fix was given a distance")
	}
	// The last fix is measured from the one before the gap.
	want := greatCircle(-33.9, 151.2, -33.9005, 151.201) +
		greatCircle(-33.9005, 151.201, -33.91, 151.22) +
		greatCircle(-33.91, 151.22, -33.99, 151.18)
	if got := tr.Samples[4].Distance; math.Abs(got-want) > 1e-6 {
		t.Errorf("distance %v m, want %v", got, want)
	}
	// A degree of latitude is about 111.2 km on the mean sphere.
	if d := greatCircle(0, 10, 1, 10); math.Abs(d-111_195) > 1 {
		t.Errorf("a degree of latitude is %v m", d)
	}
	if tr.FillDistanceFromGPS() {
		t.Error("filled again over a distance already there")
	}
}

// A recorded distance is never mixed with a summed one.
func TestFillDistanceFromGPSKeepsARecordedDistance(t *testing.T) {
	tr := &Track{Samples: []Sample{
		{HasGPS: true, Lat: 0, Lon: 0},
		{HasGPS: true, Lat: 0, Lon: 1, HasDistance: true, Distance: 5},
	}}
	if tr.FillDistanceFromGPS() {
		t.Error("filled a track that records a distance")
	}
	if tr.Samples[0].HasDistance {
		t.Error("a sample was given a summed distance beside a recorded one")
	}
}

// ridden is a track of samples a second apart from t0, each at the given
// distance in metres -- NaN for a sample with none.
func ridden(t0 time.Time, metres ...float64) *Track {
	t := &Track{}
	for i, d := range metres {
		s := Sample{Time: t0.Add(time.Duration(i) * time.Second)}
		if !math.IsNaN(d) {
			s.Distance, s.HasDistance = d, true
		}
		t.Samples = append(t.Samples, s)
	}
	return t
}

// A steady pace gives every sample that speed, at the stretch's ends too,
// where the window is cut short; a sample without a distance gets none.
func TestFillSpeedFromDistanceSteady(t *testing.T) {
	var ds []float64
	for i := range 60 {
		ds = append(ds, 3*float64(i))
	}
	ds[30] = math.NaN()
	tr := ridden(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), ds...)
	if !tr.FillSpeedFromDistance() {
		t.Fatal("no speed filled")
	}
	for i, s := range tr.Samples {
		if i == 30 {
			if s.HasSpeed {
				t.Errorf("the sample with no distance has a speed of %v", s.Speed)
			}
			continue
		}
		if !s.HasSpeed || math.Abs(s.Speed-3) > 1e-9 {
			t.Errorf("sample %d: speed %v (has %v), want 3", i, s.Speed, s.HasSpeed)
		}
	}
}

// A track with any recorded speed is left as it is.
func TestFillSpeedFromDistanceLeavesRecordedSpeed(t *testing.T) {
	tr := ridden(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 0, 5, 10, 15)
	tr.Samples[2].Speed, tr.Samples[2].HasSpeed = 9, true
	if tr.FillSpeedFromDistance() {
		t.Error("speed filled beside a recorded one")
	}
	if tr.Samples[1].HasSpeed {
		t.Error("a sample without recorded speed was given one")
	}
}

// A speed is measured within an unbroken stretch: either side of a gap
// in the recording, each stretch keeps its own pace, and a lone sample has
// no speed at all. A distance that runs backwards gives no speed.
func TestFillSpeedFromDistanceStaysInItsStretch(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tr := ridden(t0, 0, 2, 4, 6, 8, 10)
	// Five seconds without a sample: longer than a recording a second
	// apart allows, shorter than half the window, which would otherwise
	// reach across it.
	later := ridden(t0.Add(10*time.Second), 100, 104, 108, 112, 116, 120)
	alone := ridden(t0.Add(20*time.Minute), 500)
	tr.Samples = append(append(tr.Samples, later.Samples...), alone.Samples...)
	tr.FillSpeedFromDistance()
	if s := tr.Samples[5]; s.Speed != 2 {
		t.Errorf("the first stretch's last sample: %v, want its own 2 m/s", s.Speed)
	}
	if s := tr.Samples[6]; s.Speed != 4 {
		t.Errorf("the second stretch's first sample: %v, want its own 4 m/s", s.Speed)
	}
	if s := tr.Samples[12]; s.HasSpeed {
		t.Errorf("a lone sample has a speed of %v", s.Speed)
	}

	reset := ridden(t0, 50, 60, 0, 10)
	reset.FillSpeedFromDistance()
	for i, s := range reset.Samples {
		if s.HasSpeed && s.Speed < 0 {
			t.Errorf("sample %d: a negative speed, %v", i, s.Speed)
		}
	}
	if reset.Samples[1].HasSpeed {
		t.Errorf("a window across the reset gave %v", reset.Samples[1].Speed)
	}
}
