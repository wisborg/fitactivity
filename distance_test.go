package fitactivity

import (
	"math"
	"testing"
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
