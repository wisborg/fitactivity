// Package workout finds the structure of a recorded workout: where it
// stopped, the stretches of steady effort between, the levels those
// efforts fall into, the sets of repeated intervals among them, and the
// climbs and descents along the way.
//
// It answers "what was this workout?" from the recording alone -- a
// warm-up, nine intervals of five minutes at one effort with recoveries
// between, a cool-down -- and leaves the words to its caller: a dashboard,
// a summary, a map may each say it differently, in different units, and
// the structure is the part they share.
//
// Effort is measured by the best signal the activity has (see Signal):
// power where it records some, grade-adjusted pace for a run or a walk
// without, speed, and heart rate last of all, since it lags the effort it
// follows by the better part of a minute.
package workout

import (
	"time"

	"github.com/wisborg/fitactivity"
)

// Options tune the analysis. The zero value is the defaults.
type Options struct {
	// Power is the source power is read from, as every consumer of this
	// module reads it: by default a Stryd footpod's where there is one.
	Power fitactivity.PowerSource
	// Elevation tunes the elevation model climbs and descents are found
	// on; the zero
	// value is fitactivity's own defaults. A caller that tunes the model
	// for a profile passes the same here, so a climb and the profile it
	// is drawn on agree.
	Elevation fitactivity.ElevationOptions
	// MinClimbGain is the least a climb gains, and a descent loses, in
	// metres; 0 is DefaultMinClimbGain.
	MinClimbGain float64
	// MinClimbGrade is the least a climb or a descent averages, as a
	// fraction; 0 is DefaultMinClimbGrade.
	MinClimbGrade float64
	// Air, when not nil, is the air each sample i was run through: the
	// wind against the runner at their height, in m/s (negative a
	// tailwind), and its density, in kg/m³. A run's effort is then
	// measured by its grade- and wind-adjusted speed, the speed on the
	// flat in still air that costs the same. Nil is still air at sea
	// level.
	Air func(i int) (head, density float64, ok bool)
}

// Analysis is a workout's structure.
type Analysis struct {
	// Signal is what effort was measured by, and the unit of every Effort
	// below; empty where the activity has nothing to measure it by, and
	// there are no Segments.
	Signal Signal
	// Stops are where the activity did not move, in order.
	Stops []Stop
	// Segments are the stretches of steady effort between the stops, in
	// order: every moving moment of the activity is in one, but for a few
	// seconds shuffled between two stops, too short to be an effort.
	Segments []Segment
	// Levels are the efforts the segments fall into, lowest first.
	Levels []Level
	// Sets are the runs of repeated intervals: a segment harder than what
	// is either side of it, at much the same effort, again and again.
	Sets []Set
	// Climbs are the climbs along the way, in order.
	Climbs []Climb
	// Descents are the descents along the way, in order.
	Descents []Descent
}

// Span is a stretch of the activity: when it began and ended, and the
// distance it covered where the activity records distance.
type Span struct {
	Start, End                 time.Time
	StartDistance, EndDistance float64 // m
	HasDistance                bool
}

// Duration is how long the span lasted.
func (s Span) Duration() time.Duration { return s.End.Sub(s.Start) }

// Distance is the distance the span covered, in metres, and whether the
// activity records it.
func (s Span) Distance() (float64, bool) { return s.EndDistance - s.StartDistance, s.HasDistance }

// Stop is a stretch the activity did not move: standing still, or paused.
type Stop struct {
	Span
	// Paused says the recording was paused for it, rather than recording
	// someone standing still.
	Paused bool
}

// Segment is a stretch of steady effort.
type Segment struct {
	Span
	// Effort is the segment's mean effort, in Signal's unit.
	Effort float64
	// Level is the index in Analysis.Levels of the level it falls in.
	Level int
	// The segment's means of the readings the activity has, each with
	// whether it had any: power (from Options.Power), speed in metres a
	// second, heart rate in beats a minute.
	Power, Speed, HeartRate          float64
	HasPower, HasSpeed, HasHeartRate bool
}

// Level is an effort a workout's segments return to.
type Level struct {
	// Effort is its segments' mean effort, by time.
	Effort float64
	// Duration is the time spent at it.
	Duration time.Duration
}

// Set is a run of repeated intervals: hard stretches of much the same
// effort, each harder than what is before and after it, one after
// another, with recoveries between.
type Set struct {
	// Intervals are the hard stretches, in order.
	Intervals []Interval
	// Recoveries are the stretches between them, one fewer than
	// Intervals: whatever lay between one interval's end and the next's
	// start, moving or not.
	Recoveries []Span
}

// Interval is one hard stretch of a set: one segment, or several
// neighbours of nearly its effort -- an interval whose target stepped up
// partway is still one interval.
type Interval struct {
	Span
	// First and Last are the indices in Analysis.Segments of its first
	// and last segment.
	First, Last int
	// Effort is its mean effort, by time.
	Effort float64
	// The interval's means of the readings the activity has, by time, as
	// a Segment's are.
	Power, Speed, HeartRate          float64
	HasPower, HasSpeed, HasHeartRate bool
}

// Climb is a stretch of road that rose, with its dips.
type Climb struct {
	Span
	// Gain is how much it rose, start to top, in metres; Grade its mean
	// grade, as a fraction.
	Gain, Grade float64
	// StartElevation is the elevation it began at, in metres.
	StartElevation float64
	// Lat and Lon are where it began, where the activity has a fix there:
	// a loop's laps meet the same hill at the same place.
	Lat, Lon float64
	HasGPS   bool
}

// Descent is a stretch of road that fell, with its rises: a climb run the
// other way.
type Descent struct {
	Span
	// Loss is how much it fell, top to bottom, in metres; Grade its mean
	// grade downhill, as a positive fraction.
	Loss, Grade float64
	// StartElevation is the elevation it began at, its top, in metres.
	StartElevation float64
	// Lat and Lon are where it began, where the activity has a fix there.
	Lat, Lon float64
	HasGPS   bool
}

// Analyse finds the structure of t.
func Analyse(t *fitactivity.Track, opts Options) Analysis {
	var a Analysis
	if t == nil || len(t.Samples) < 2 {
		return a
	}
	elev := fitactivity.BuildElevationModel(t, opts.Elevation)
	sig, effort := chooseSignal(t, opts.Power, elev, opts.Air)
	a.Signal = sig
	a.Stops = findStops(t)
	if sig != "" {
		a.Segments, a.Levels = segment(t, effort, a.Stops, opts.Power)
		// A hike or a walk is not an interval session, and its stops --
		// a view, a photograph, a gate -- between stretches of a steep
		// path walked briskly look like one; sets are not looked for.
		if !walked(t.Sport) {
			a.Sets = sets(a.Segments, a.Stops)
		}
	}
	a.Climbs, a.Descents = climbs(t, elev, opts)
	return a
}
