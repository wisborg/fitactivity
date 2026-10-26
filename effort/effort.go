// Package effort is what running costs, by the energy it takes: on a hill,
// by Minetti and others' measure, and against the air, by Pugh's -- so a
// stretch run uphill or into the wind can be compared with one run on the
// flat in still air, at the speed on the flat in still air that costs the
// same.
//
// Both are measurements on runners, not first principles, and both are
// averages over the runners measured. A runner bigger or smaller than the
// one the air's cost is scaled for, or one more or less economical on a
// hill, pays a little more or less; the comparison between stretches of
// one run is what they are good for, not an account of the watts.
package effort

import "math"

// HillCost is the energy running costs on grade (a fraction), in joules
// per kilogram per metre, by Minetti and others' measure on a treadmill
// (Journal of Applied Physiology, 2002): a polynomial in the grade, valid
// from -45% to +45%, outside which it is held at its ends. A treadmill has
// no air to push through, so this is the cost without the air's.
func HillCost(grade float64) float64 {
	i := math.Max(-0.45, math.Min(0.45, grade))
	return 155.4*math.Pow(i, 5) - 30.4*math.Pow(i, 4) - 43.3*math.Pow(i, 3) + 46.3*i*i + 19.5*i + 3.6
}

// AirCoefficient is what pushing through the air costs, in joules per
// kilogram per metre for each (m/s)² of the air's speed past the runner,
// at SeaLevelDensity.
//
// It is Pugh's measure (Journal of Physiology, 1971), as the drafting
// studies of Hoogkamer, Kram and others use it: the oxygen the air costs is
// 0.00354 · A · v³ litres a minute through still air, with A the runner's
// frontal area, 0.45 m² for a runner of 58 kg and 1.71 m. At 20.9 kJ a
// litre of oxygen that is 0.0096 · v² joules per kilogram per metre, which
// is di Prampero's figure of about 0.01 for the same thing, found another
// way. A heavier runner's frontal area grows more slowly than their mass,
// so they pay a little less per kilogram; the same figure is used for all.
const AirCoefficient = 0.0096

// SeaLevelDensity is the air's density AirCoefficient is for, in kg/m³:
// the standard atmosphere's at sea level and 15 °C.
const SeaLevelDensity = 1.225

// AirDensity is the density of dry air at pressure (hPa) and temperature
// (°C), in kg/m³. Humid air is a little lighter; at a run's temperatures
// by under one per cent, which is well inside the rest of the model.
func AirDensity(pressure, temperature float64) float64 {
	const rDry = 287.05 // J/(kg·K)
	return pressure * 100 / (rDry * (temperature + 273.15))
}

// Cost is the energy running at speed costs on grade, with the air moving
// against the runner at head (m/s; negative a tailwind), of density, in
// joules per kilogram per metre: the hill's cost and the air's. The air's
// is its speed past the runner squared, with its sign, so a tailwind
// faster than the runner pushes them along.
func Cost(speed, grade, head, density float64) float64 {
	air := speed + head
	return HillCost(grade) + AirCoefficient*density/SeaLevelDensity*air*math.Abs(air)
}

// Equivalent is the speed on the flat in still air, at SeaLevelDensity,
// that costs the same energy a second as speed on grade into head through
// air of density: the effort, as a speed. Zero for a speed of zero.
func Equivalent(speed, grade, head, density float64) float64 {
	if speed <= 0 {
		return 0
	}
	power := Cost(speed, grade, head, density) * speed // W/kg
	if power <= 0 {
		// Downhill steep enough, or a gale behind, to cost nothing: no
		// speed on the flat is that easy.
		return 0
	}
	flat := func(u float64) float64 { return Cost(u, 0, 0, SeaLevelDensity) * u }
	return solve(flat, power)
}

// StillAir is the speed on grade, in still air of density, that costs the
// same energy a second as speed into head: what the wind's absence would
// have given for the same effort.
func StillAir(speed, grade, head, density float64) float64 {
	if speed <= 0 {
		return 0
	}
	power := Cost(speed, grade, head, density) * speed
	if power <= 0 {
		return speed
	}
	still := func(u float64) float64 { return Cost(u, grade, 0, density) * u }
	if still(1e-3) >= power {
		return speed
	}
	return solve(still, power)
}

// solve is the speed at which f, rising with it, reaches power: by
// bisection, between nothing and a speed no runner reaches.
func solve(f func(float64) float64, power float64) float64 {
	lo, hi := 0.0, 20.0
	for f(hi) < power {
		hi *= 2
		if hi > 1000 {
			return hi
		}
	}
	for range 60 {
		mid := (lo + hi) / 2
		if f(mid) < power {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// Exposure is the share of the wind forecast at 10 m that reaches a runner
// a metre or so off the ground, by the logarithmic profile wind follows
// above ground of a given roughness: the speed at a height grows with its
// logarithm over the roughness length.
type Exposure float64

const (
	// Open is open country -- fields, a beach, a moor -- with a roughness
	// length of 3 cm: ln(1/0.03)/ln(10/0.03).
	Open Exposure = 0.60
	// Suburban is houses, gardens and trees, with a roughness length of
	// 25 cm: ln(1/0.25)/ln(10/0.25).
	Suburban Exposure = 0.38
	// Urban is between buildings taller than the runner, below which the
	// profile no longer holds; a quarter is a common working figure, and
	// a street along the wind can funnel far more.
	Urban Exposure = 0.25
)
