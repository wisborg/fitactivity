package fitactivity

import "math"

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
