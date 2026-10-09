package fitactivity

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// fr24Flight is an invented flight in a Flightradar24 CSV's own layout: at
// the gate, taxiing, airborne, and one report with no position. The place
// and the callsign are made up.
const fr24Flight = fr24Header + `
1785002293,2026-07-25T17:58:13Z,XYZ123,"-33.9000,151.2000",0,0,14
1785002353,2026-07-25T17:59:13Z,XYZ123,"-33.9005,151.2010",0,12,120
1785002413,2026-07-25T18:00:13Z,XYZ123,"-33.9100,151.2200",1500,160,190
1785002473,2026-07-25T18:01:13Z,XYZ123,,9800,250,191
1785002533,2026-07-25T18:02:13Z,XYZ123,"-33.9900,151.1800",32000,512,191
`

func TestReadFR24(t *testing.T) {
	tr := readTrack(t, writeCourse(t, "flight.csv", fr24Flight))
	if len(tr.Samples) != 5 {
		t.Fatalf("%d samples, want 5", len(tr.Samples))
	}
	if tr.Sport != "flying" {
		t.Errorf("sport %q, want flying", tr.Sport)
	}
	s := tr.Samples[4]
	if !s.Time.Equal(time.Date(2026, 7, 25, 18, 2, 13, 0, time.UTC)) {
		t.Errorf("time %v", s.Time)
	}
	if !s.HasGPS || s.Lat != -33.99 || s.Lon != 151.18 {
		t.Errorf("position %v,%v (has %v)", s.Lat, s.Lon, s.HasGPS)
	}
	if !s.HasElevation || math.Abs(s.Elevation-9753.6) > 1e-6 {
		t.Errorf("elevation %v m (has %v), want 32,000 ft = 9753.6 m", s.Elevation, s.HasElevation)
	}
	if !s.HasSpeed || math.Abs(s.Speed-512*1852.0/3600) > 1e-9 {
		t.Errorf("speed %v m/s (has %v), want 512 kt", s.Speed, s.HasSpeed)
	}
	if gap := tr.Samples[3]; gap.HasGPS {
		t.Errorf("a report with no position has a fix at %v,%v", gap.Lat, gap.Lon)
	}
	if tr.Samples[0].HasDistance {
		t.Error("Read gave a distance the file does not record")
	}
}

// Only Flightradar24's own header makes a CSV something Read takes.
func TestReadRefusesOtherCSV(t *testing.T) {
	path := writeCourse(t, "other.csv", "time,lat,lon\n2026-07-25T17:58:13Z,-33.9,151.2\n")
	if _, err := Read(path); err == nil {
		t.Error("a CSV that is not Flightradar24's was read")
	}
	bad := strings.Replace(fr24Flight, `"-33.9100,151.2200"`, `"-33.9100"`, 1)
	if _, err := Read(writeCourse(t, "bad.csv", bad)); err == nil || errors.Is(err, ErrNoTimes) {
		t.Errorf("a position with no longitude: %v", err)
	}
}
