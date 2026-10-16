package workout

import (
	"math"
	"sort"

	"github.com/wisborg/fitactivity"
)

// The defaults for what counts as a climb, or a descent: five metres of
// gain -- or loss -- at three per cent. A runner feels five metres; a
// loop in a park has climbs no bigger, and they are what makes it the
// hilly loop.
const (
	DefaultMinClimbGain  = 5.0
	DefaultMinClimbGrade = 0.03
)

// climbStep is the distance the elevation profile is read at, in metres.
const climbStep = 5.0

// climbFlat is how far the road may run without a new top before the
// climb is over, in metres: a climb ends on a long flat as surely as on a
// descent, or a bump, a kilometre of flat and a hill would be one gentle
// climb, too gentle to be any.
const climbFlat = 200.0

// climbs are the climbs along t's elevation profile, and its descents:
// see rises.
func climbs(t *fitactivity.Track, elev *fitactivity.ElevationModel, opts Options) ([]Climb, []Descent) {
	if elev.Empty() {
		return nil, nil
	}
	minGain, minGrade := opts.MinClimbGain, opts.MinClimbGrade
	if minGain <= 0 {
		minGain = DefaultMinClimbGain
	}
	if minGrade <= 0 {
		minGrade = DefaultMinClimbGrade
	}
	var ds, es, fall []float64
	for d := elev.StartDistance(); d <= elev.TotalDistance(); d += climbStep {
		e, _, _ := elev.AtDistance(d)
		ds, es, fall = append(ds, d), append(es, e), append(fall, -e)
	}
	var cs []Climb
	for _, r := range rises(ds, es, minGain, minGrade) {
		h := hillAt(t, ds[r[0]], ds[r[1]])
		cs = append(cs, Climb{Span: h.Span, Gain: es[r[1]] - es[r[0]], Grade: (es[r[1]] - es[r[0]]) / (ds[r[1]] - ds[r[0]]),
			StartElevation: es[r[0]], Lat: h.lat, Lon: h.lon, HasGPS: h.hasGPS})
	}
	// A descent is a climb of the profile turned upside down: the same
	// rules, the same least height and grade, so a hill climbed and run
	// back down is a climb and a descent alike.
	var dsc []Descent
	for _, r := range rises(ds, fall, minGain, minGrade) {
		h := hillAt(t, ds[r[0]], ds[r[1]])
		dsc = append(dsc, Descent{Span: h.Span, Loss: es[r[0]] - es[r[1]], Grade: (es[r[0]] - es[r[1]]) / (ds[r[1]] - ds[r[0]]),
			StartElevation: es[r[0]], Lat: h.lat, Lon: h.lon, HasGPS: h.hasGPS})
	}
	return cs, dsc
}

// rises are the climbs of the profile es, read every climbStep along ds,
// as the indices of each one's start and top. A climb runs from a low
// point up to a top, and ends once the road has fallen back from the top
// by a fifth of what the climb had gained, or by two metres if that is
// more -- so a dip partway up does not end it, and a real descent does --
// or has run climbFlat without a new top. One that gains less than
// minGain, or averages less than minGrade, is no climb.
func rises(ds, es []float64, minGain, minGrade float64) [][2]int {
	var out [][2]int
	low := 0
	for low < len(es)-1 {
		top, j := low, low
		for j < len(es) {
			if es[j] > es[top] {
				top = j
			}
			gain := es[top] - es[low]
			if es[j] < es[low] || es[top]-es[j] > math.Max(2, gain/5) || ds[j]-ds[top] > climbFlat {
				break
			}
			j++
		}
		// The climb starts where the road leaves its low point, not where
		// the flat before it began: a kilometre of flat ahead of a hill
		// is no part of it, and would halve its grade.
		start := low
		for k := top; k > low; k-- {
			if es[k] <= es[low]+0.5 {
				start = k
				break
			}
		}
		gain, run := es[top]-es[start], ds[top]-ds[start]
		if gain >= minGain && run > 0 && gain/run >= minGrade {
			out = append(out, [2]int{start, top})
		}
		if j >= len(es) {
			break
		}
		// The next climb starts from the lowest point before the road
		// rises again.
		next := j
		for k := j; k < len(es) && es[k] <= es[next]+0.5; k++ {
			if es[k] < es[next] {
				next = k
			}
		}
		if next <= low {
			next = low + 1
		}
		low = next
	}
	return out
}

// hill is where a climb or a descent is: its span, and its first fix.
type hill struct {
	Span
	lat, lon float64
	hasGPS   bool
}

// hillAt is the stretch from distance d0 to d1 along t: its times and
// place found from the samples that reach those distances.
func hillAt(t *fitactivity.Track, d0, d1 float64) hill {
	at := func(d float64) fitactivity.Sample {
		i := sort.Search(len(t.Samples), func(i int) bool {
			return t.Samples[i].HasDistance && t.Samples[i].Distance >= d
		})
		if i >= len(t.Samples) {
			i = len(t.Samples) - 1
		}
		return t.Samples[i]
	}
	a, b := at(d0), at(d1)
	h := hill{Span: Span{Start: a.Time, End: b.Time, StartDistance: d0, EndDistance: d1, HasDistance: true}}
	if a.HasGPS {
		h.lat, h.lon, h.hasGPS = a.Lat, a.Lon, true
	}
	return h
}
