package units

import (
	"math"
	"strings"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

// Every unit converts by its definition: a mile is 1609.344 m, a nautical
// mile 1852 m, a foot 0.3048 m, a knot a nautical mile an hour; and back.
func TestConversions(t *testing.T) {
	for _, c := range []struct {
		u        Unit
		si, want float64
	}{
		{Kilometre, 5000, 5},
		{Mile, 1609.344, 1},
		{NauticalMile, 3704, 2},
		{Metre, 12, 12},
		{Foot, 0.3048 * 1000, 1000},
		{KilometresPerHour, 10, 36},
		{MilesPerHour, 0.44704, 1},
		{Knot, 1852.0 / 3600 * 450, 450},
		{MetresPerSecond, 3.5, 3.5},
		{MinutesPerKilometre, 1000.0 / 300, 300}, // 3.33 m/s is 5:00/km
		{MinutesPerMile, 1609.344 / 480, 480},    // 8:00/mi
	} {
		if got := c.u.FromSI(c.si); !near(got, c.want) {
			t.Errorf("%v of SI in %s is %v, want %v", c.si, c.u.Name, got, c.want)
		}
		if back := c.u.ToSI(c.want); !near(back, c.si) {
			t.Errorf("%v %s back in SI is %v, want %v", c.want, c.u.Name, back, c.si)
		}
	}
	if !math.IsInf(MinutesPerKilometre.FromSI(0), 1) || !math.IsInf(MinutesPerKilometre.FromSI(-2), 1) || !math.IsInf(MinutesPerMile.ToSI(-1), 1) {
		t.Error("standing still has a pace")
	}
}

// A system is its units; one quantity's unit can then be changed alone, as a
// flight's are; a unit not of that quantity, a quantity with no units, and a
// system there is not, are refused with what there is.
func TestSets(t *testing.T) {
	m, err := Of(Metric)
	if err != nil || m != (Set{Kilometre, Metre, KilometresPerHour, MinutesPerKilometre}) {
		t.Errorf("metric: %+v %v", m, err)
	}
	i, err := Of(Imperial)
	if err != nil || i != (Set{Mile, Foot, MilesPerHour, MinutesPerMile}) {
		t.Errorf("imperial: %+v %v", i, err)
	}
	flight := i
	if err := flight.Use(Distance, "nmi"); err != nil {
		t.Fatal(err)
	}
	if err := flight.Use(Speed, "kn"); err != nil {
		t.Fatal(err)
	}
	if metres := i; metres.Use(Elevation, "m") != nil || metres.Elevation != Metre || metres.Distance != Mile {
		t.Errorf("imperial with its elevation in metres: %+v", metres)
	}
	if flight != (Set{NauticalMile, Foot, Knot, MinutesPerMile}) || flight.Unit(Speed) != Knot || flight.Unit(Elevation) != Foot || flight.Unit(Pace) != MinutesPerMile || flight.Unit(Distance) != NauticalMile {
		t.Errorf("a flight's units: %+v", flight)
	}
	for _, c := range []struct {
		q    Quantity
		name string
		want string
	}{
		{Distance, "ft", `"ft" is not a unit of distance; use km, mi, nmi`},
		{Speed, "kph", "use km/h, mph, kn, m/s"},
		{"time", "s", "use distance, elevation, speed, pace"},
	} {
		s := m
		if err := s.Use(c.q, c.name); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Use(%s, %s): %v, want %q", c.q, c.name, err, c.want)
		}
	}
	if _, err := Of("nautical"); err == nil || !strings.Contains(err.Error(), "use metric or imperial") {
		t.Errorf("a system there is not: %v", err)
	}
}

// A pace is minutes and seconds per the unit's distance, rounded to the
// second; a speed whole from 10 up and to a tenth below.
func TestFormat(t *testing.T) {
	for _, c := range []struct {
		got, want string
	}{
		{FormatPace(1000.0/330, MinutesPerKilometre), "5:30/km"},
		{FormatPace(1609.344/479.6, MinutesPerMile), "8:00/mi"},
		{FormatPace(0, MinutesPerKilometre), "--:--/km"},
		{FormatSpeed(5, KilometresPerHour), "18 km/h"},
		{FormatSpeed(250, Knot), "486 kn"},
		{FormatSpeed(2.5, MetresPerSecond), "2.5 m/s"},
		{FormatSpeed(2.5, MilesPerHour), "5.6 mph"},
	} {
		if c.got != c.want {
			t.Errorf("%q, want %q", c.got, c.want)
		}
	}
}
