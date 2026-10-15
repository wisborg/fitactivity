package workout

import (
	"math"
	"strings"
	"time"

	"github.com/wisborg/fitactivity"
)

// Signal is what a workout's effort is measured by.
type Signal string

const (
	// SignalPower is power, in watts.
	SignalPower Signal = "power"
	// SignalGradeAdjustedSpeed is speed as it would have been on the
	// flat for the same effort, in metres a second: a run's or a walk's
	// effort where it records no power, which its speed alone misstates
	// on every hill.
	SignalGradeAdjustedSpeed Signal = "grade-adjusted speed"
	// SignalSpeed is speed, in metres a second.
	SignalSpeed Signal = "speed"
	// SignalHeartRate is heart rate, in beats a minute: the last resort,
	// since it follows an effort by the better part of a minute and so
	// places every change late.
	SignalHeartRate Signal = "heart rate"
)

// enough is the share of an activity's samples a signal must cover to
// measure its effort by: one present for a moment is no measure of the
// rest.
const enough = 0.5

// chooseSignal is the best signal t measures effort by, and each sample's
// effort by it. The order is the signals' faithfulness to effort: power
// is effort; grade-adjusted speed is a runner's effort to within the
// model's error, where there is elevation to adjust for; speed is a
// rider's only on the flat and in still air; heart rate lags.
func chooseSignal(t *fitactivity.Track, power fitactivity.PowerSource, elev *fitactivity.ElevationModel) (Signal, func(i int) (float64, bool)) {
	covers := func(read func(fitactivity.Sample) bool) bool {
		n := 0
		for _, s := range t.Samples {
			if read(s) {
				n++
			}
		}
		return float64(n) >= enough*float64(len(t.Samples))
	}
	if covers(func(s fitactivity.Sample) bool { _, ok := s.ResolvedPower(power); return ok }) {
		return SignalPower, func(i int) (float64, bool) { return t.Samples[i].ResolvedPower(power) }
	}
	hasSpeed := covers(func(s fitactivity.Sample) bool { return s.HasSpeed })
	if hasSpeed && onFoot(t.Sport) && !elev.Empty() {
		return SignalGradeAdjustedSpeed, func(i int) (float64, bool) {
			s := t.Samples[i]
			if !s.HasSpeed || !s.HasDistance {
				return 0, false
			}
			return gradeAdjusted(s.Speed, elev.GradeAtDistance(s.Distance, gradeWindow)), true
		}
	}
	if hasSpeed {
		return SignalSpeed, func(i int) (float64, bool) { return t.Samples[i].Speed, t.Samples[i].HasSpeed }
	}
	if covers(func(s fitactivity.Sample) bool { return s.HasHeartRate }) {
		return SignalHeartRate, func(i int) (float64, bool) {
			return float64(t.Samples[i].HeartRate), t.Samples[i].HasHeartRate
		}
	}
	return "", nil
}

// onFoot says sport is one grade-adjusted speed is a model of: running,
// walking, hiking.
func onFoot(sport string) bool {
	s := strings.ToLower(sport)
	return strings.Contains(s, "run") || s == "walking" || s == "hiking"
}

// walked says sport is walking or hiking.
func walked(sport string) bool {
	s := strings.ToLower(sport)
	return s == "walking" || s == "hiking"
}

// gradeWindow is the distance either side of a sample its grade is
// measured over, in metres: long enough that a barometer's wobble is not
// a hill, short enough that a short steep ramp is one.
const gradeWindow = 25

// gradeAdjusted is speed on grade as the speed on the flat that costs the
// same, by Minetti and others' measure of the energy running costs on a
// slope (Journal of Applied Physiology, 2002): a polynomial in the grade,
// valid from -45% to +45%, outside which it is held at its ends.
func gradeAdjusted(speed, grade float64) float64 {
	return speed * runningCost(grade) / runningCost(0)
}

// runningCost is the energy running costs, in joules per kilogram per
// metre, at grade (a fraction).
func runningCost(grade float64) float64 {
	i := math.Max(-0.45, math.Min(0.45, grade))
	return 155.4*math.Pow(i, 5) - 30.4*math.Pow(i, 4) - 43.3*math.Pow(i, 3) + 46.3*i*i + 19.5*i + 3.6
}

// Standing still is moving slower than stillSpeed for at least stillFor;
// a gap of at least stillFor between samples, longer than the
// recording's own rhythm allows, is a pause.
const (
	stillSpeed = 0.5 // m/s
	stillFor   = 10 * time.Second
)

// findStops are the stretches t did not move: the gaps in its recording
// -- a watch paused, or out of coverage -- and, where it records speed,
// the stretches it stood still. A track that records no speed has only
// its gaps: nothing else says it stopped.
func findStops(t *fitactivity.Track) []Stop {
	gap := max(t.GapTolerance(), stillFor)
	span := func(from, to fitactivity.Sample) Span {
		return Span{Start: from.Time, End: to.Time, StartDistance: from.Distance, EndDistance: to.Distance,
			HasDistance: from.HasDistance && to.HasDistance}
	}
	var stops []Stop
	still := -1 // the first sample of a stretch standing still
	closeStill := func(end int) {
		if still >= 0 && t.Samples[end].Time.Sub(t.Samples[still].Time) >= stillFor {
			stops = append(stops, Stop{Span: span(t.Samples[still], t.Samples[end])})
		}
		still = -1
	}
	for i, s := range t.Samples {
		if i > 0 && s.Time.Sub(t.Samples[i-1].Time) > gap {
			closeStill(i - 1)
			stops = append(stops, Stop{Span: span(t.Samples[i-1], s), Paused: true})
		}
		if s.HasSpeed && s.Speed < stillSpeed {
			if still < 0 {
				still = i
			}
			continue
		}
		if still >= 0 {
			closeStill(i)
		}
	}
	if still >= 0 {
		closeStill(len(t.Samples) - 1)
	}
	return stops
}
