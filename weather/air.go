package weather

import (
	"math"
	"sort"
	"time"

	"github.com/wisborg/fitactivity"
	"github.com/wisborg/fitactivity/effort"
)

// Air is the air each sample of t was run through, by the report: the
// wind against the runner at their height -- the 10 m wind scaled by
// exposure, turned to the sample's heading -- and the air's density from
// its pressure and temperature. A sample with no heading, or no weather,
// has none. The shape is workout.Options.Air's.
func Air(r *Report, t *fitactivity.Track, exposure effort.Exposure) func(i int) (head, density float64, ok bool) {
	heading, hasHeading := Headings(t)
	return func(i int) (float64, float64, bool) {
		if i < 0 || i >= len(t.Samples) || !hasHeading[i] {
			return 0, 0, false
		}
		s := t.Samples[i]
		c, ok := r.At(s.Lat, s.Lon, s.HasGPS, s.Time)
		if !ok {
			return 0, 0, false
		}
		head, _ := Relative(c, heading[i])
		return head * float64(exposure), effort.AirDensity(c.Pressure, c.Temperature), true
	}
}

// WindCost is the time the wind cost over t, by the report: for every
// moving second, how much faster the same effort would have gone in still
// air, on the grade grade(i) gives. Negative where the wind helped more
// than it hindered; an out and back in a steady wind costs a little,
// since a headwind costs more than the same tailwind gives back. ok is
// false where no moving sample had weather and a heading.
func WindCost(r *Report, t *fitactivity.Track, exposure effort.Exposure, grade func(i int) float64) (time.Duration, bool) {
	air := Air(r, t, exposure)
	var cost float64
	ok := false
	for i := 1; i < len(t.Samples); i++ {
		s := t.Samples[i]
		dt := s.Time.Sub(t.Samples[i-1].Time).Seconds()
		if !s.HasSpeed || s.Speed < 1 || dt <= 0 || dt > 10 {
			continue
		}
		head, density, has := air(i)
		if !has {
			continue
		}
		still := effort.StillAir(s.Speed, grade(i), head, density)
		cost += dt * (1 - s.Speed/still)
		ok = true
	}
	return time.Duration(cost * float64(time.Second)), ok
}

// WindPace is how much the wind cost on the pace at its worst and gave at
// its best over t, in seconds a metre: the still-air pace for the same
// effort less the pace run, positive into the wind and negative with it
// behind. Not the extremes of single seconds, which a corner's heading or
// a gust's second set, but the 95th and 5th percentiles of the moving
// seconds that had weather and a heading; with no tailwind ever, best is
// still the smallest cost. ok is false where none had.
func WindPace(r *Report, t *fitactivity.Track, exposure effort.Exposure, grade func(i int) float64) (worst, best float64, ok bool) {
	air := Air(r, t, exposure)
	var costs []float64
	for i, s := range t.Samples {
		if !s.HasSpeed || s.Speed < 1 {
			continue
		}
		head, density, has := air(i)
		if !has {
			continue
		}
		still := effort.StillAir(s.Speed, grade(i), head, density)
		costs = append(costs, 1/s.Speed-1/still)
	}
	if len(costs) == 0 {
		return 0, 0, false
	}
	sort.Float64s(costs)
	at := func(q float64) float64 { return costs[int(q*float64(len(costs)-1)+0.5)] }
	return at(0.95), at(0.05), true
}

// FootpodWind is the wind a Stryd footpod's air power says the runner met:
// where it blew from, in degrees, and how strongly that shows -- the
// variation of air power with heading, as a share of its mean. Air power
// is what the pod reckons the whole body spent against the air, from what
// it senses at the shoe, a few centimetres off the ground; so its
// direction is evidence and its size is not a wind speed at any height.
// ok is false without the developer field, or with too little variation
// to tell a direction from the route's own shelter and the pod's whole
// watts: a calm day's.
func FootpodWind(t *fitactivity.Track) (from, strength float64, ok bool) {
	heading, hasHeading := Headings(t)
	var mean, c, s, n float64
	for i, smp := range t.Samples {
		ap, has := smp.DevFields[footpodAirPower]
		if !has || !hasHeading[i] || !smp.HasSpeed || smp.Speed < 1.5 {
			continue
		}
		r := ap / (smp.Speed * smp.Speed * smp.Speed)
		h := heading[i] * math.Pi / 180
		mean += r
		c += r * math.Cos(h)
		s += r * math.Sin(h)
		n++
	}
	if n < minFootpodSamples || mean <= 0 {
		return 0, 0, false
	}
	mean, c, s = mean/n, 2*c/n, 2*s/n
	strength = math.Hypot(c, s) / mean
	if strength < minFootpodStrength {
		return 0, strength, false
	}
	return math.Mod(math.Atan2(s, c)*180/math.Pi+360, 360), strength, true
}

// footpodAirPower is the developer field a Stryd pod records its air power
// under, in watts.
const footpodAirPower = "Air Power"

// minFootpodSamples is ten minutes of moving seconds, and
// minFootpodStrength the variation below which a direction is noise: the
// calm run it was tuned on varied by about a fifth with heading and two
// fits put its wind 40° apart; the windy one by over a half, and they
// agreed within 5°.
const (
	minFootpodSamples  = 600
	minFootpodStrength = 0.35
)

// Apart is how far apart two directions are, in degrees, 0 to 180.
func Apart(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 360)
	return math.Min(d, 360-d)
}

// HeatCost is the time the heat, or the cold, cost over t by the report,
// for a runner not acclimatised to it: each moving second slower by the
// share effort.HeatLoss gives for the WBGT then, from the temperature and
// humidity. lo and hi are the coolest and warmest WBGT met; ok is false
// where no moving sample had weather.
func HeatCost(r *Report, t *fitactivity.Track) (cost time.Duration, lo, hi float64, ok bool) {
	var secs float64
	for i := 1; i < len(t.Samples); i++ {
		s := t.Samples[i]
		dt := s.Time.Sub(t.Samples[i-1].Time).Seconds()
		if !s.HasSpeed || s.Speed < 1 || dt <= 0 || dt > 10 {
			continue
		}
		c, has := r.At(s.Lat, s.Lon, s.HasGPS, s.Time)
		if !has {
			continue
		}
		w := effort.WBGT(c.Temperature, c.Humidity)
		if !ok || w < lo {
			lo = w
		}
		if !ok || w > hi {
			hi = w
		}
		ok = true
		secs += dt * effort.HeatLoss(w)
	}
	return time.Duration(secs * float64(time.Second)), lo, hi, ok
}

// AltitudeCost is the time the altitude cost over t, for a runner not
// acclimatised to it: each moving second slower than the same share of
// their oxygen uptake would have run at sea level, at the sample's own
// elevation -- the report's ground where the track records none -- in the
// air the report gives, on grade(i). highest is the highest point; ok is
// false where no moving sample had a height.
func AltitudeCost(r *Report, t *fitactivity.Track, exposure effort.Exposure, grade func(i int) float64) (cost time.Duration, highest float64, ok bool) {
	air := Air(r, t, exposure)
	var secs float64
	for i := 1; i < len(t.Samples); i++ {
		s := t.Samples[i]
		dt := s.Time.Sub(t.Samples[i-1].Time).Seconds()
		if !s.HasSpeed || s.Speed < 1 || dt <= 0 || dt > 10 {
			continue
		}
		h, has := s.Elevation, s.HasElevation
		if !has {
			h, has = r.ElevationAt(s.Lat, s.Lon, s.HasGPS)
		}
		if !has {
			continue
		}
		if !ok || h > highest {
			highest = h
		}
		ok = true
		loss := effort.AltitudeLoss(h)
		if loss <= 0 {
			continue
		}
		head, density, hasAir := air(i)
		if !hasAir {
			head, density = 0, effort.SeaLevelDensity
		}
		secs += dt * (1 - s.Speed/effort.AtSeaLevel(s.Speed, grade(i), head, density, loss))
	}
	return time.Duration(secs * float64(time.Second)), highest, ok
}
