package fitactivity

import (
	"math"
	"time"
)

// earthRadius is the mean radius of the Earth, in metres: the sphere the
// distance between two fixes is measured over.
const earthRadius = 6_371_008.8

// FillDistanceFromGPS gives every positioned sample of t the distance
// travelled to it along the fixes, in metres from the first, when no sample
// of t carries a recorded distance; it reports whether it did.
//
// Read computes nothing to stand in for what a file did not record, and
// still does not: a GPX, a KML or a Flightradar24 flight has no distance,
// and a Track read from one says so. This is the caller asking for one by
// name, for the panels and models -- a profile against distance, splits --
// that have nothing else to measure along. It is the distance a device
// would have summed from the same fixes, and so has a device's fault too:
// a fix that wanders while nothing moves adds to it. A sample without a fix
// is left without a distance, as it would be in a recording; a track with
// any recorded distance at all is left as it is, recorded distance and
// summed distance never mixed in one.
//
// Distances are along the surface of a sphere, so the length of a flight
// across an ocean is its length over the ground, not across a flat map.
func (t *Track) FillDistanceFromGPS() bool {
	for _, s := range t.Samples {
		if s.HasDistance {
			return false
		}
	}
	var total float64
	var prev *Sample
	filled := false
	for i := range t.Samples {
		s := &t.Samples[i]
		if !s.HasGPS {
			continue
		}
		if prev != nil {
			total += greatCircle(prev.Lat, prev.Lon, s.Lat, s.Lon)
		}
		s.Distance, s.HasDistance = total, true
		prev, filled = s, true
	}
	return filled
}

// greatCircle is the distance between two places on the sphere, in metres.
func greatCircle(lat0, lon0, lat1, lon1 float64) float64 {
	const rad = math.Pi / 180
	dLat, dLon := (lat1-lat0)*rad, (lon1-lon0)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat0*rad)*math.Cos(lat1*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadius * math.Asin(math.Min(1, math.Sqrt(a)))
}

// speedWindow is how much time FillSpeedFromDistance measures each sample's
// speed over: half of it either side. A machine that records its distance
// in whole metres a second -- iFit's do -- moves at one or two of them a
// second on a walk, so a speed from one second to the next jumps between
// whole numbers; over ten seconds it reads to a tenth of a metre a second,
// and a change of pace still shows within the window it happened in.
const speedWindow = 10 * time.Second

// FillSpeedFromDistance gives every sample of t that it can a speed worked
// out from the distance it records -- the distance covered over speedWindow
// centred on the sample, by the time it took -- when no sample of t carries
// a recorded speed; it reports whether it did.
//
// Like FillDistanceFromGPS, this is the caller asking for what the file
// did not record: an indoor machine's TCX, a GPX, records distance but no
// speed, and the speed and pace a dashboard shows have nothing else to read.
// It is measured only within an unbroken stretch of recording: the window
// is cut short at a gap longer than t's GapTolerance, so a speed is never
// spread across time nobody recorded, and a sample with no other sample
// within its window has none. A distance that runs backwards -- a reset --
// gives no speed rather than a negative one. A track with any recorded
// speed at all is left as it is, recorded and worked-out speed never mixed
// in one.
func (t *Track) FillSpeedFromDistance() bool {
	for _, s := range t.Samples {
		if s.HasSpeed {
			return false
		}
	}
	gap := t.GapTolerance()
	half := speedWindow / 2
	// The samples with a distance, and where each unbroken stretch of them
	// begins.
	var idx []int
	for i, s := range t.Samples {
		if s.HasDistance {
			idx = append(idx, i)
		}
	}
	runStart := make([]int, len(idx))
	for k := range idx {
		if k > 0 && t.Samples[idx[k]].Time.Sub(t.Samples[idx[k-1]].Time) <= gap {
			runStart[k] = runStart[k-1]
		} else {
			runStart[k] = k
		}
	}
	filled := false
	lo, hi := 0, 0
	for k, i := range idx {
		at := t.Samples[i].Time
		// The window's ends: the furthest samples within half of it either
		// side, in the same stretch.
		lo = max(lo, runStart[k])
		for t.Samples[idx[lo]].Time.Before(at.Add(-half)) {
			lo++
		}
		hi = max(hi, k)
		for hi+1 < len(idx) && runStart[hi+1] == runStart[k] && !t.Samples[idx[hi+1]].Time.After(at.Add(half)) {
			hi++
		}
		a, b := t.Samples[idx[lo]], t.Samples[idx[hi]]
		dt := b.Time.Sub(a.Time).Seconds()
		dd := b.Distance - a.Distance
		if dt <= 0 || dd < 0 {
			continue
		}
		s := &t.Samples[i]
		s.Speed, s.HasSpeed = dd/dt, true
		filled = true
	}
	return filled
}
