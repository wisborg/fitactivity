package fitactivity

import "fmt"

type tcxFile struct {
	Activities []struct {
		Sport string `xml:"Sport,attr"`
		Laps  []struct {
			StartTime string     `xml:"StartTime,attr"`
			Tracks    []tcxTrack `xml:"Track"`
		} `xml:"Lap"`
	} `xml:"Activities>Activity"`
	Courses []struct {
		Tracks []tcxTrack `xml:"Track"`
	} `xml:"Courses>Course"`
}

type tcxTrack struct {
	Points []tcxPoint `xml:"Trackpoint"`
}

type tcxPoint struct {
	Time     string `xml:"Time"`
	Position *struct {
		Lat string `xml:"LatitudeDegrees"`
		Lon string `xml:"LongitudeDegrees"`
	} `xml:"Position"`
	Altitude  *string `xml:"AltitudeMeters"`
	Distance  *string `xml:"DistanceMeters"`
	HeartRate *struct {
		Value string `xml:"Value"`
	} `xml:"HeartRateBpm"`
	Cadence    *string `xml:"Cadence"`
	Extensions *xmlAny `xml:"Extensions"`
}

// readTCX reads a Training Center file's activities: every trackpoint of
// every track of every lap, which carry a time by the schema's rule, and a
// distance as FIT does. Speed, run cadence and power come from Garmin's
// ActivityExtension.
//
// Only when there is no activity are its courses read, and a course is a
// plan -- see recording.planned. Its trackpoints carry times too, but they
// are the pace the course was drawn at, not anybody's.
//
// A TCX file carries no pause events and no elapsed total -- a lap's
// TotalTimeSeconds is timer time only -- so the timing is its first lap's
// start, and a TimerModel built on it measures to the last sample.
func readTCX(data []byte) (*recording, error) {
	var x tcxFile
	if err := decodeXML(data, &x); err != nil {
		return nil, fmt.Errorf("decoding TCX: %w", err)
	}
	rec := &recording{}
	for _, a := range x.Activities {
		if rec.sport == "" {
			rec.sport = sportName(a.Sport)
		}
		for _, l := range a.Laps {
			if l.StartTime != "" {
				start, err := parseTime(l.StartTime)
				if err != nil {
					return nil, err
				}
				if rec.timing.Start.IsZero() || start.Before(rec.timing.Start) {
					rec.timing.Start = start
				}
			}
			if err := rec.addTCX(l.Tracks); err != nil {
				return nil, err
			}
		}
	}
	if len(rec.points) > 0 {
		return rec, nil
	}
	rec.planned = true
	rec.timing = ActivityTiming{}
	for _, c := range x.Courses {
		if err := rec.addTCX(c.Tracks); err != nil {
			return nil, err
		}
	}
	return rec, nil
}

func (rec *recording) addTCX(tracks []tcxTrack) error {
	for _, t := range tracks {
		for _, p := range t.Points {
			f, err := p.fix()
			if err != nil {
				return err
			}
			rec.points = append(rec.points, f)
		}
	}
	return nil
}

func (p tcxPoint) fix() (fix, error) {
	var f fix
	var err error
	if p.Time != "" {
		if f.Time, err = parseTime(p.Time); err != nil {
			return f, err
		}
		f.timed = true
	}
	// A trackpoint without a position is a sensor reading taken with no
	// fix -- indoors, or before the GPS has one -- and is kept, as Decode
	// keeps a Record without one.
	if p.Position != nil {
		if f.Lat, f.Lon, err = position(p.Position.Lat, p.Position.Lon); err != nil {
			return f, err
		}
		f.HasGPS = true
	}
	if f.Elevation, f.HasElevation, err = optFloat(p.Altitude, "altitude"); err != nil {
		return f, err
	}
	if f.Distance, f.HasDistance, err = optFloat(p.Distance, "distance"); err != nil {
		return f, err
	}
	ext := &xmlAny{}
	if p.HeartRate != nil {
		ext.Children = append(ext.Children, xmlAny{Text: p.HeartRate.Value, XMLName: xmlName("hr")})
	}
	if p.Cadence != nil {
		ext.Children = append(ext.Children, xmlAny{Text: *p.Cadence, XMLName: xmlName("cadence")})
	}
	if p.Extensions != nil {
		ext.Children = append(ext.Children, *p.Extensions)
	}
	if err := f.extensions(ext); err != nil {
		return f, err
	}
	return f, nil
}
