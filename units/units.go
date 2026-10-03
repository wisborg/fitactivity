// Package units is the units an activity's numbers are shown in: distance,
// elevation, speed and pace, each in the units commonly used for it, chosen
// by a system -- metric or imperial -- or one by one, for the mixtures some
// activities read in: a flight has its altitude in feet, its distance in
// nautical miles and its speed in knots.
//
// Everything in this module is in SI -- metres, seconds, metres a second --
// and stays so. This package only converts at the edge, for a person to
// read, so that videofx, fitdash and course show one number for one file.
package units

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Quantity is a kind of number with units to choose from.
type Quantity string

const (
	// Distance is how far: along a course, between distance markers.
	Distance Quantity = "distance"
	// Elevation is how high, and also how far over short distances, such
	// as how far a run strayed from a course: the same units, metres or
	// feet.
	Elevation Quantity = "elevation"
	// Speed is how fast.
	Speed Quantity = "speed"
	// Pace is how long a distance takes: how fast, the way a runner reads
	// it.
	Pace Quantity = "pace"
)

// Quantities are every Quantity, in the order a listing gives them.
var Quantities = []Quantity{Distance, Elevation, Speed, Pace}

// Unit is one unit of a Quantity.
type Unit struct {
	// Name is how the unit is written: km, ft, km/h, min/mi.
	Name     string
	Quantity Quantity
	// si is how much of the SI unit -- metres for distance and elevation,
	// metres a second for speed, metres for pace's distance -- one of this
	// unit is.
	si float64
}

// The units there are. Pace units are a time per distance unit; their si is
// the distance's metres.
var (
	Kilometre    = Unit{"km", Distance, 1000}
	Mile         = Unit{"mi", Distance, 1609.344}
	NauticalMile = Unit{"nmi", Distance, 1852}

	Metre = Unit{"m", Elevation, 1}
	Foot  = Unit{"ft", Elevation, 0.3048}

	KilometresPerHour = Unit{"km/h", Speed, 1000.0 / 3600}
	MilesPerHour      = Unit{"mph", Speed, 1609.344 / 3600}
	Knot              = Unit{"kn", Speed, 1852.0 / 3600}
	MetresPerSecond   = Unit{"m/s", Speed, 1}

	MinutesPerKilometre = Unit{"min/km", Pace, 1000}
	MinutesPerMile      = Unit{"min/mi", Pace, 1609.344}
)

// All are every unit, by quantity.
var All = map[Quantity][]Unit{
	Distance:  {Kilometre, Mile, NauticalMile},
	Elevation: {Metre, Foot},
	Speed:     {KilometresPerHour, MilesPerHour, Knot, MetresPerSecond},
	Pace:      {MinutesPerKilometre, MinutesPerMile},
}

// FromSI is v, in SI, in u: a distance in metres as kilometres, a speed in
// metres a second as knots. For a pace unit, v is a speed in metres a second
// and the result is the seconds it takes to cover one of u's distance; a
// speed of 0 or less takes forever, +Inf.
func (u Unit) FromSI(v float64) float64 {
	if u.Quantity == Pace {
		if !(v > 0) {
			return math.Inf(1)
		}
		return u.si / v
	}
	return v / u.si
}

// ToSI is FromSI undone: v in u, as metres, or metres a second -- for a pace
// unit, v seconds per u's distance as a speed.
func (u Unit) ToSI(v float64) float64 {
	if u.Quantity == Pace {
		if !(v > 0) {
			return math.Inf(1)
		}
		return u.si / v
	}
	return v * u.si
}

// System is a set of units chosen together.
type System string

const (
	Metric   System = "metric"
	Imperial System = "imperial"
)

// Systems are every System.
var Systems = []System{Metric, Imperial}

// Set is the unit in use for each quantity.
type Set struct {
	Distance, Elevation, Speed, Pace Unit
}

// Of is the units of system s.
func Of(s System) (Set, error) {
	switch s {
	case Metric:
		return Set{Kilometre, Metre, KilometresPerHour, MinutesPerKilometre}, nil
	case Imperial:
		return Set{Mile, Foot, MilesPerHour, MinutesPerMile}, nil
	}
	return Set{}, fmt.Errorf("%q is not a system of units; use %s", s, joinSystems())
}

// Use makes the unit named name the one in use for quantity q.
func (s *Set) Use(q Quantity, name string) error {
	units, ok := All[q]
	if !ok {
		return fmt.Errorf("%q is not a quantity with units; use %s", q, joinQuantities())
	}
	i := slices.IndexFunc(units, func(u Unit) bool { return u.Name == name })
	if i < 0 {
		return fmt.Errorf("%q is not a unit of %s; use %s", name, q, joinUnits(units))
	}
	switch q {
	case Distance:
		s.Distance = units[i]
	case Elevation:
		s.Elevation = units[i]
	case Speed:
		s.Speed = units[i]
	case Pace:
		s.Pace = units[i]
	}
	return nil
}

// Unit is the unit in use for q.
func (s Set) Unit(q Quantity) Unit {
	switch q {
	case Distance:
		return s.Distance
	case Elevation:
		return s.Elevation
	case Speed:
		return s.Speed
	}
	return s.Pace
}

// FormatPace is a speed in metres a second as a pace in u, minutes and
// seconds per u's distance and u's name -- "5:30/km" -- rounded to the
// second; "--:--" and the name for a speed of 0 or less, which has no pace.
func FormatPace(speed float64, u Unit) string {
	per := strings.TrimPrefix(u.Name, "min")
	if !(speed > 0) {
		return "--:--" + per
	}
	s := int(math.Round(u.FromSI(speed)))
	return fmt.Sprintf("%d:%02d%s", s/60, s%60, per)
}

// FormatSpeed is a speed in metres a second in u, and u's name -- "18 km/h"
// -- in whole units from 10 up and to a tenth below, where a whole unit is
// too coarse to tell two speeds apart.
func FormatSpeed(speed float64, u Unit) string {
	v := u.FromSI(speed)
	if math.Abs(v) >= 10 {
		return fmt.Sprintf("%.0f %s", v, u.Name)
	}
	return fmt.Sprintf("%.1f %s", v, u.Name)
}

func joinSystems() string {
	var s []string
	for _, x := range Systems {
		s = append(s, string(x))
	}
	return strings.Join(s, " or ")
}

func joinQuantities() string {
	var s []string
	for _, q := range Quantities {
		s = append(s, string(q))
	}
	return strings.Join(s, ", ")
}

func joinUnits(us []Unit) string {
	var s []string
	for _, u := range us {
		s = append(s, u.Name)
	}
	return strings.Join(s, ", ")
}
