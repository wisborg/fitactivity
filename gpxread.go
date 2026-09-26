package fitactivity

import "fmt"

type gpxFile struct {
	Tracks []struct {
		Type     string `xml:"type"`
		Segments []struct {
			Points []gpxPoint `xml:"trkpt"`
		} `xml:"trkseg"`
	} `xml:"trk"`
	Routes []struct {
		Type   string     `xml:"type"`
		Points []gpxPoint `xml:"rtept"`
	} `xml:"rte"`
}

type gpxPoint struct {
	Lat        string  `xml:"lat,attr"`
	Lon        string  `xml:"lon,attr"`
	Ele        *string `xml:"ele"`
	Time       *string `xml:"time"`
	Speed      *string `xml:"speed"`
	Extensions *xmlAny `xml:"extensions"`
}

// readGPX reads a GPX 1.0 or 1.1 file: its tracks, every segment of every
// one, in the file's order. Only when it has no track points at all are its
// routes read instead, and a route is a plan -- see recording.planned --
// whatever times its points carry. Waypoints are places marked on the way,
// not the way, and are not read.
//
// Segments are not kept apart. A break between two is a gap in the
// recording, which a Track already shows as the absence of samples across
// it; Track.AtWithGap is what declines to draw a line over one.
func readGPX(data []byte) (*recording, error) {
	var g gpxFile
	if err := decodeXML(data, &g); err != nil {
		return nil, fmt.Errorf("decoding GPX: %w", err)
	}
	rec := &recording{}
	for _, t := range g.Tracks {
		if rec.sport == "" {
			rec.sport = sportName(t.Type)
		}
		for _, seg := range t.Segments {
			for _, p := range seg.Points {
				f, err := p.fix()
				if err != nil {
					return nil, err
				}
				rec.points = append(rec.points, f)
			}
		}
	}
	if len(rec.points) > 0 {
		return rec, nil
	}
	rec.planned = true
	for _, r := range g.Routes {
		if rec.sport == "" {
			rec.sport = sportName(r.Type)
		}
		for _, p := range r.Points {
			f, err := p.fix()
			if err != nil {
				return nil, err
			}
			rec.points = append(rec.points, f)
		}
	}
	return rec, nil
}

func (p gpxPoint) fix() (fix, error) {
	var f fix
	lat, lon, err := position(p.Lat, p.Lon)
	if err != nil {
		return f, err
	}
	f.Lat, f.Lon, f.HasGPS = lat, lon, true
	if f.Elevation, f.HasElevation, err = optFloat(p.Ele, "elevation"); err != nil {
		return f, err
	}
	// GPX 1.0 put speed on the point itself; 1.1 moved it into extensions.
	if f.Speed, f.HasSpeed, err = optFloat(p.Speed, "speed"); err != nil {
		return f, err
	}
	if p.Time != nil && *p.Time != "" {
		if f.Time, err = parseTime(*p.Time); err != nil {
			return f, err
		}
		f.timed = true
	}
	if err := f.extensions(p.Extensions); err != nil {
		return f, err
	}
	return f, nil
}
