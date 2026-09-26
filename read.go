package fitactivity

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/muktihari/fit/profile/typedef"
)

// ErrNoTimes is what Read reports for a course whose points do not all carry
// the time they were recorded at: a planned route, a line drawn on a map, a
// GPX route, a TCX course. Such a file is a perfectly good course to map or
// summarise -- ReadRoute reads it -- but it is not a recording, and a Track is
// one. Every Sample in a Track has a real Time, and videofx and fitdash both
// animate by it; a Track made from a file without times would have to invent
// them, and would then play a planned route at a speed nobody moved at.
var ErrNoTimes = errors.New("the course does not carry the time of every point")

// Route is a course's geometry without a clock: the positions it passes
// through, in order, and nothing that happened along them.
//
// It is what ReadRoute returns for every format, whether the file was
// recorded or planned, and it is a separate type from Track rather than a
// Track with a flag so that nothing which animates a Track can be handed one
// by accident. A caller that wants times asks Read, and is told plainly when
// there are none.
type Route struct {
	// SourcePath is the file the route was read from.
	SourcePath string
	// Sport is the activity's sport in the FIT profile's vocabulary
	// ("running", "cycling", ...), or empty when the file names none this
	// package recognises.
	Sport string
	// Points are the positions in the course's own order: by time for a
	// recording, and in the file's order for a plan. Only positions are
	// here -- a recorded sample with no fix contributes nothing.
	Points []RoutePoint
}

// RoutePoint is one position on a Route.
type RoutePoint struct {
	Lat, Lon float64 // degrees
	// HasElevation reports whether Elevation is a height the file gave, in
	// metres above sea level. A KML coordinate's third value is only that
	// when its altitudeMode is absolute; clamped to the ground, which is
	// KML's default, the number is ignored by every KML reader and so here.
	HasElevation bool
	Elevation    float64
}

// Route is the geometry of t: its positioned samples, in time order.
func (t *Track) Route() *Route {
	r := &Route{SourcePath: t.SourcePath, Sport: t.Sport}
	for _, s := range t.Samples {
		if s.HasGPS {
			r.Points = append(r.Points, RoutePoint{Lat: s.Lat, Lon: s.Lon, HasElevation: s.HasElevation, Elevation: s.Elevation})
		}
	}
	return r
}

// Read reads the recorded activity at path, telling FIT, GPX, TCX, KML and
// KMZ apart by what the file holds rather than by its name.
//
// A FIT file is read by Decode. The others carry less: no timer events, no
// session totals, and -- except TCX -- no recorded distance, so a Track read
// from one simply has those absent, and the models built on it fall back as
// they do for a FIT file without them. Nothing is computed to stand in for a
// field the file did not record.
//
// A file whose points do not all carry a time is refused with an error that
// wraps ErrNoTimes; see ReadRoute for its geometry.
func Read(path string) (*Track, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	f, err := sniff(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f == formatFIT {
		return Decode(path)
	}
	rec, err := readRecording(f, data)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return rec.track(path)
}

// ReadRoute reads the geometry of the course at path, in any format Read
// takes, whether or not the file carries times.
func ReadRoute(path string) (*Route, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	f, err := sniff(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f == formatFIT {
		t, err := Decode(path)
		if err != nil {
			return nil, err
		}
		return t.Route(), nil
	}
	rec, err := readRecording(f, data)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return rec.route(path), nil
}

type format int

const (
	formatFIT format = iota
	formatGPX
	formatTCX
	formatKML
	formatKMZ
)

// sniff tells the formats apart by content. A FIT file says ".FIT" at byte 8
// of its header, a KMZ is a zip, and the three XML formats are told apart by
// their root element, whatever namespace or version it declares.
func sniff(data []byte) (format, error) {
	if len(data) >= 12 && string(data[8:12]) == ".FIT" {
		return formatFIT, nil
	}
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return formatKMZ, nil
	}
	dec := xml.NewDecoder(bytes.NewReader(withoutBOM(data)))
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0, errors.New("not a FIT, GPX, TCX, KML or KMZ file")
		}
		if se, ok := tok.(xml.StartElement); ok {
			switch se.Name.Local {
			case "gpx":
				return formatGPX, nil
			case "TrainingCenterDatabase":
				return formatTCX, nil
			case "kml":
				return formatKML, nil
			}
			return 0, fmt.Errorf("an XML file of <%s>, not GPX, TCX or KML", se.Name.Local)
		}
	}
}

func withoutBOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
}

func decodeXML(data []byte, v any) error {
	return xml.NewDecoder(bytes.NewReader(withoutBOM(data))).Decode(v)
}

// recording is what the XML readers agree on: the points of a course in the
// file's order, each with or without a time, and what the file says about
// the activity as a whole.
type recording struct {
	sport  string
	points []fix
	timing ActivityTiming
	// planned is a course whose times, if it has any, are a plan's: a GPX
	// route, a TCX course. They are the pace somebody meant to go at, and
	// are not read as a recording.
	planned bool
}

// fix is one point of a recording. Its Sample's Time is meaningful only when
// timed is true.
type fix struct {
	Sample
	timed bool
}

func readRecording(f format, data []byte) (*recording, error) {
	switch f {
	case formatGPX:
		return readGPX(data)
	case formatTCX:
		return readTCX(data)
	case formatKML:
		return readKML(data)
	case formatKMZ:
		return readKMZ(data)
	}
	return nil, fmt.Errorf("no reader for format %d", f)
}

func (r *recording) track(path string) (*Track, error) {
	if len(r.points) == 0 {
		return nil, fmt.Errorf("%s holds no track points", path)
	}
	untimed := 0
	for _, p := range r.points {
		if !p.timed {
			untimed++
		}
	}
	switch {
	case r.planned:
		return nil, fmt.Errorf("%s is a planned course, not a recording: %w", path, ErrNoTimes)
	case untimed > 0:
		return nil, fmt.Errorf("%s: %d of its %d points carry no time: %w", path, untimed, len(r.points), ErrNoTimes)
	}
	samples := make([]Sample, len(r.points))
	for i, p := range r.points {
		samples[i] = p.Sample
	}
	// Sorted, as Decode sorts: the Track's invariant, and the order every
	// interpolation over it relies on. Stable, so points recorded at the
	// same instant keep the file's order.
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].Time.Before(samples[j].Time) })
	// A TCX lap starts with the point the one before it ended on, the same
	// reading written twice. Kept, it would be a sample the device took
	// once counted as two; only an exact repeat goes, so two different
	// readings at one instant both stay, as Decode keeps them.
	kept := samples[:1]
	for _, s := range samples[1:] {
		if !reflect.DeepEqual(s, kept[len(kept)-1]) {
			kept = append(kept, s)
		}
	}
	samples = kept
	return &Track{
		SourcePath: path,
		Sources:    []string{path},
		Sport:      r.sport,
		Samples:    samples,
		Timing:     r.timing,
	}, nil
}

// route is the recording's geometry: a recording's in time order through the
// Track it makes, so a route and the track read from one file agree point for
// point, and anything else in the file's order.
func (r *recording) route(path string) *Route {
	if t, err := r.track(path); err == nil {
		return t.Route()
	}
	rt := &Route{SourcePath: path, Sport: r.sport}
	for _, p := range r.points {
		if p.HasGPS {
			rt.Points = append(rt.Points, RoutePoint{Lat: p.Lat, Lon: p.Lon, HasElevation: p.HasElevation, Elevation: p.Elevation})
		}
	}
	return rt
}

// sportName puts a file's own name for a sport into the FIT profile's
// vocabulary, the one Track.Sport speaks: "Running" and "running" become
// "running", TCX's "Biking" becomes "cycling". A name the profile does not
// have is left out rather than passed through, so a consumer matching on the
// profile's names never meets one it cannot know.
func sportName(s string) string {
	s = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "_")
	switch s {
	case "biking", "bike", "ride":
		s = "cycling"
	case "run":
		s = "running"
	case "walk":
		s = "walking"
	case "hike":
		s = "hiking"
	}
	if sp := typedef.SportFromString(s); sp != typedef.SportInvalid {
		return sp.String()
	}
	return ""
}

// parseTime reads an XML dateTime. Only a full date and time with a zone is a
// time: one without a zone is ambiguous by the offset nobody wrote down, and
// is an error rather than a guess at UTC.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a date and time with a zone", s)
	}
	return t.UTC(), nil
}

// optFloat reads an optional number: absent or empty is absent, anything else
// must be a finite number.
func optFloat(s *string, what string) (float64, bool, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return 0, false, nil
	}
	return number(*s, what)
}

func number(s, what string) (float64, bool, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false, fmt.Errorf("%s %q is not a number", what, s)
	}
	return v, true, nil
}

// position reads a latitude and longitude, refusing one off the globe.
func position(lat, lon string) (float64, float64, error) {
	la, _, err := number(lat, "latitude")
	if err != nil {
		return 0, 0, err
	}
	lo, _, err := number(lon, "longitude")
	if err != nil {
		return 0, 0, err
	}
	if la < -90 || la > 90 || lo < -180 || lo > 180 {
		return 0, 0, fmt.Errorf("%s,%s is not a position on the globe", lat, lon)
	}
	return la, lo, nil
}

// xmlAny is an element of any name, for the extension blocks whose contents
// vary by whichever program wrote them.
type xmlAny struct {
	XMLName  xml.Name
	Text     string   `xml:",chardata"`
	Children []xmlAny `xml:",any"`
}

func xmlName(local string) xml.Name { return xml.Name{Local: local} }

// extensions reads the sensor values an extensions block carries, by the
// local names Garmin's TrackPointExtension and ActivityExtension use and the
// few others that are common, at whatever depth and in whatever namespace.
// The first value for each is taken. A value out of the range a Sample holds
// it in is an error, not a truncation.
func (s *Sample) extensions(ext *xmlAny) error {
	if ext == nil {
		return nil
	}
	for _, c := range ext.Children {
		if len(c.Children) > 0 {
			if err := s.extensions(&c); err != nil {
				return err
			}
			continue
		}
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		name := strings.ToLower(c.XMLName.Local)
		v, _, err := number(text, name)
		if err != nil {
			return err
		}
		inRange := func(lo, hi float64) error {
			if v < lo || v > hi {
				return fmt.Errorf("%s %s is out of range", name, text)
			}
			return nil
		}
		switch name {
		case "hr", "heartrate":
			if s.HasHeartRate {
				continue
			}
			if err := inRange(0, math.MaxUint8); err != nil {
				return err
			}
			s.HeartRate, s.HasHeartRate = uint8(math.Round(v)), true
		case "cad", "cadence", "runcadence":
			if s.HasCadence {
				continue
			}
			if err := inRange(0, math.MaxUint8); err != nil {
				return err
			}
			s.Cadence, s.HasCadence = uint8(math.Round(v)), true
		case "atemp", "temp", "temperature":
			if s.HasTemperature {
				continue
			}
			if err := inRange(math.MinInt8, math.MaxInt8); err != nil {
				return err
			}
			s.Temperature, s.HasTemperature = int8(math.Round(v)), true
		case "power", "watts":
			if s.HasPower {
				continue
			}
			if err := inRange(0, math.MaxUint16); err != nil {
				return err
			}
			s.Power, s.HasPower = uint16(math.Round(v)), true
		case "speed":
			if s.HasSpeed {
				continue
			}
			if err := inRange(0, math.Inf(1)); err != nil {
				return err
			}
			s.Speed, s.HasSpeed = v, true
		}
	}
	return nil
}

// kmzDocument is the KML inside a KMZ: by the format's rule the first .kml
// file at the archive's root, and failing that the first anywhere in it.
func kmzDocument(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("reading the KMZ archive: %w", err)
	}
	var pick *zip.File
	for _, f := range zr.File {
		if !strings.EqualFold(path.Ext(f.Name), ".kml") {
			continue
		}
		if !strings.Contains(f.Name, "/") {
			pick = f
			break
		}
		if pick == nil {
			pick = f
		}
	}
	if pick == nil {
		return nil, errors.New("the KMZ archive holds no .kml file")
	}
	rc, err := pick.Open()
	if err != nil {
		return nil, fmt.Errorf("opening %s in the KMZ archive: %w", pick.Name, err)
	}
	defer rc.Close()
	// Read to a limit, since the size an archive declares is the archive's
	// word: a KML of a gigabyte is no course anybody recorded.
	doc, err := io.ReadAll(io.LimitReader(rc, maxKML+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s in the KMZ archive: %w", pick.Name, err)
	}
	if len(doc) > maxKML {
		return nil, fmt.Errorf("%s in the KMZ archive is over %d MiB", pick.Name, maxKML>>20)
	}
	return doc, nil
}

const maxKML = 1 << 30
