package weather

import "github.com/wisborg/fitactivity"

// headingSpan is how far either side of a sample its heading is measured
// over: far enough that a fix's wander of a few metres does not turn it,
// near enough that a corner shows within a few seconds of running.
const headingSpan = 15.0

// Headings is the direction each sample of t was moving in, in degrees
// clockwise from north, and whether it has one: the file's own where it
// records it, and otherwise the bearing from the fix headingSpan metres
// behind to the one headingSpan metres ahead. A sample with no fix either
// side, or standing still, has none.
func Headings(t *fitactivity.Track) (deg []float64, ok []bool) {
	n := len(t.Samples)
	deg, ok = make([]float64, n), make([]bool, n)
	for i, s := range t.Samples {
		if s.HasCourse {
			deg[i], ok[i] = s.Course, true
			continue
		}
		if !s.HasGPS {
			continue
		}
		back, ahead := -1, -1
		for j := i - 1; j >= 0; j-- {
			if p := t.Samples[j]; p.HasGPS && greatCircle(p.Lat, p.Lon, s.Lat, s.Lon) >= headingSpan {
				back = j
				break
			}
		}
		for j := i + 1; j < n; j++ {
			if p := t.Samples[j]; p.HasGPS && greatCircle(s.Lat, s.Lon, p.Lat, p.Lon) >= headingSpan {
				ahead = j
				break
			}
		}
		switch {
		case back >= 0 && ahead >= 0:
			b, a := t.Samples[back], t.Samples[ahead]
			deg[i], ok[i] = bearing(b.Lat, b.Lon, a.Lat, a.Lon), true
		case ahead >= 0:
			a := t.Samples[ahead]
			deg[i], ok[i] = bearing(s.Lat, s.Lon, a.Lat, a.Lon), true
		case back >= 0:
			b := t.Samples[back]
			deg[i], ok[i] = bearing(b.Lat, b.Lon, s.Lat, s.Lon), true
		}
	}
	return deg, ok
}
