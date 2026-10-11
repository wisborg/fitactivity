package fitactivity

import (
	"fmt"
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// kmlContainer is a kml root, Document or Folder, any of which holds the
// others and Placemarks, to any depth.
type kmlContainer struct {
	Name        string         `xml:"name"`
	Description string         `xml:"description"`
	Documents   []kmlContainer `xml:"Document"`
	Folders     []kmlContainer `xml:"Folder"`
	Placemarks  []kmlPlacemark `xml:"Placemark"`
}

type kmlPlacemark struct {
	Description string `xml:"description"`
	TimeStamp   *struct {
		When string `xml:"when"`
	} `xml:"TimeStamp"`
	TimeSpan *struct {
		Begin string `xml:"begin"`
		End   string `xml:"end"`
	} `xml:"TimeSpan"`
	kmlGeometry
}

// kmlGeometry is what a Placemark or a MultiGeometry holds. Track and
// MultiTrack are the gx: extension's; element names are matched without
// their namespace, so KML 2.0 to 2.2 read alike.
type kmlGeometry struct {
	Points      []kmlCoords   `xml:"Point"`
	Lines       []kmlCoords   `xml:"LineString"`
	Tracks      []kmlTrack    `xml:"Track"`
	MultiTracks []kmlMulti    `xml:"MultiTrack"`
	Multi       []kmlGeometry `xml:"MultiGeometry"`
}

type kmlCoords struct {
	AltitudeMode []string `xml:"altitudeMode"`
	Coordinates  string   `xml:"coordinates"`
}

type kmlTrack struct {
	AltitudeMode []string `xml:"altitudeMode"`
	When         []string `xml:"when"`
	Coords       []string `xml:"coord"`
}

type kmlMulti struct {
	AltitudeMode []string   `xml:"altitudeMode"`
	Tracks       []kmlTrack `xml:"Track"`
}

// kmlParts sorts a KML file's geometry by what it can say about the course.
type kmlParts struct {
	timed   []fix // gx:Track points, and Points with a TimeStamp or TimeSpan
	lines   []fix // LineStrings, in document order
	markers []fix // Points with no time
}

// readKML reads the course a KML file draws. KML is a drawing format, and one
// file often draws the same course twice -- Garmin Connect writes every fix
// as a timed Point AND the whole course as a LineString; a flight tracker
// writes timed Points AND a trail of line segments -- so the parts are taken
// in order of what they know:
//
//  1. Timed geometry, if there is any: gx:Track, and Points carrying a
//     TimeStamp or a TimeSpan. A LineString beside them is taken to be a
//     drawing of the same course, and left out.
//  2. Otherwise the LineStrings, joined in document order: a course with no
//     times.
//  3. Otherwise the untimed Points, in order -- a course marked as places.
//     With a line present they are the places marked on it, not the course.
//
// A LineString in a Placemark with a time is still untimed: the time is the
// line's as a whole, and says nothing about when any one vertex was passed.
func readKML(data []byte) (*recording, error) {
	var k kmlContainer
	if err := decodeXML(data, &k); err != nil {
		return nil, fmt.Errorf("decoding KML: %w", err)
	}
	var parts kmlParts
	if err := parts.container(k); err != nil {
		return nil, err
	}
	rec := &recording{}
	for _, d := range k.Documents {
		if f, ok := fr24KMLFlight(d.Name, d.Description); ok {
			rec.flights = append(rec.flights, f)
		}
	}
	// A flight tracker's file is a flight's, as its CSV is.
	if len(rec.flights) > 0 {
		rec.sport = sportName("flying")
	}
	switch {
	case len(parts.timed) > 0:
		rec.points = parts.timed
	case len(parts.lines) > 0:
		rec.points = parts.lines
	default:
		rec.points = parts.markers
	}
	return rec, nil
}

func readKMZ(data []byte) (*recording, error) {
	doc, err := kmzDocument(data)
	if err != nil {
		return nil, err
	}
	return readKML(doc)
}

func (p *kmlParts) container(c kmlContainer) error {
	for _, d := range c.Documents {
		if err := p.container(d); err != nil {
			return err
		}
	}
	for _, f := range c.Folders {
		if err := p.container(f); err != nil {
			return err
		}
	}
	for _, pm := range c.Placemarks {
		if err := p.placemark(pm); err != nil {
			return err
		}
	}
	return nil
}

// placemark reads one Placemark, with the time it was reached if it says.
//
// A TimeSpan's END is the time: Garmin Connect writes each fix's span as
// running from the fix before it to this one, the stretch of the course that
// led here, so its begin is the previous fix's time and would put every point
// a sample late. A span with no end -- open, in KML's terms -- has its begin.
func (p *kmlParts) placemark(pm kmlPlacemark) error {
	var when string
	switch {
	case pm.TimeStamp != nil && strings.TrimSpace(pm.TimeStamp.When) != "":
		when = pm.TimeStamp.When
	case pm.TimeSpan != nil && strings.TrimSpace(pm.TimeSpan.End) != "":
		when = pm.TimeSpan.End
	case pm.TimeSpan != nil && strings.TrimSpace(pm.TimeSpan.Begin) != "":
		when = pm.TimeSpan.Begin
	}
	n := len(p.timed)
	if err := p.geometry(pm.kmlGeometry, when); err != nil {
		return err
	}
	if added := p.timed[n:]; len(added) > 0 {
		fr24Readings(pm.Description, added)
	}
	return nil
}

// fr24Speed and fr24Heading are a Flightradar24 placemark's ground speed and
// track, as the HTML of its pop-up writes them once the tags are gone:
// "Speed: 448 kt", "Heading: 229°".
var (
	fr24Speed   = regexp.MustCompile(`\bSpeed: ([0-9][0-9,]*(?:\.[0-9]+)?) kt\b`)
	fr24Heading = regexp.MustCompile(`\bHeading: ([0-9]+(?:\.[0-9]+)?)°`)
)

// fr24Readings gives fixes the ground speed and track a Flightradar24
// placemark's description reports, where it reports them. A Flightradar24
// KML carries everything its CSV export does -- the same reports, the same
// positions, altitudes, speeds and tracks -- but the speed and track only
// inside the HTML of each timed point's pop-up, for Google Earth. They are
// the aircraft's own, broadcast with its position, not worked out from the
// positions: from one report to the next they change by a knot or two at
// cruise, where a speed taken between the reported positions jumps by
// tens. So they are read, and the KML alone is the whole flight.
//
// Anything else's description is prose, and a field it does not have is
// absent, not zero.
func fr24Readings(description string, fixes []fix) {
	if !strings.Contains(description, "Speed:") && !strings.Contains(description, "Heading:") {
		return
	}
	// Unescaped first: Flightradar24 writes its HTML in CDATA, Garmin
	// Connect escaped, and either way the tags are to go.
	text := spaces.ReplaceAllString(htmlTag.ReplaceAllString(html.UnescapeString(description), " "), " ")
	speed, hasSpeed := fr24Field(fr24Speed, text)
	heading, hasHeading := fr24Field(fr24Heading, text)
	hasHeading = hasHeading && heading >= 0 && heading <= 360
	for i := range fixes {
		if hasSpeed {
			fixes[i].Speed, fixes[i].HasSpeed = speed*knots, true
		}
		if hasHeading {
			fixes[i].Course, fixes[i].HasCourse = math.Mod(heading, 360), true
		}
	}
}

// fr24Field is the number re finds in text, its thousands separators
// dropped.
func fr24Field(re *regexp.Regexp, text string) (float64, bool) {
	m := re.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	return v, err == nil
}

func (p *kmlParts) geometry(g kmlGeometry, when string) error {
	for _, pt := range g.Points {
		fixes, err := coordinates(pt.Coordinates, absolute(pt.AltitudeMode))
		if err != nil {
			return err
		}
		if when == "" {
			p.markers = append(p.markers, fixes...)
			continue
		}
		t, err := parseTime(when)
		if err != nil {
			return err
		}
		for i := range fixes {
			fixes[i].Time, fixes[i].timed = t, true
		}
		p.timed = append(p.timed, fixes...)
	}
	for _, l := range g.Lines {
		fixes, err := coordinates(l.Coordinates, absolute(l.AltitudeMode))
		if err != nil {
			return err
		}
		p.lines = append(p.lines, fixes...)
	}
	for _, t := range g.Tracks {
		if err := p.track(t, absolute(t.AltitudeMode)); err != nil {
			return err
		}
	}
	for _, m := range g.MultiTracks {
		for _, t := range m.Tracks {
			abs := absolute(m.AltitudeMode)
			if len(t.AltitudeMode) > 0 {
				abs = absolute(t.AltitudeMode)
			}
			if err := p.track(t, abs); err != nil {
				return err
			}
		}
	}
	for _, mg := range g.Multi {
		if err := p.geometry(mg, when); err != nil {
			return err
		}
	}
	return nil
}

// track reads a gx:Track: a when for every coord, in step. A track whose
// counts differ cannot say which time goes with which position, and is an
// error; one with no whens at all is untimed, and read as a line.
func (p *kmlParts) track(t kmlTrack, abs bool) error {
	if len(t.When) != 0 && len(t.When) != len(t.Coords) {
		return fmt.Errorf("a gx:Track with %d times for %d positions", len(t.When), len(t.Coords))
	}
	for i, c := range t.Coords {
		f, err := kmlPoint(strings.Fields(c), " ", abs)
		if err != nil {
			return err
		}
		if len(t.When) == 0 {
			p.lines = append(p.lines, f)
			continue
		}
		if f.Time, err = parseTime(t.When[i]); err != nil {
			return err
		}
		f.timed = true
		p.timed = append(p.timed, f)
	}
	return nil
}

// absolute reports whether an altitudeMode makes a coordinate's altitude a
// height above sea level. Unset is KML's default, clampToGround, under which
// the altitude is ignored; relativeToGround is a height above a ground this
// package does not know.
func absolute(modes []string) bool {
	for _, m := range modes {
		if strings.TrimSpace(m) == "absolute" {
			return true
		}
	}
	return false
}

// commaSpace is whitespace beside a comma, which KML forbids inside a tuple
// and Garmin Connect writes after every comma of its Points.
var commaSpace = regexp.MustCompile(`\s*,\s*`)

// coordinates reads a KML coordinates string: tuples of lon,lat[,alt],
// separated by whitespace. Whitespace beside a comma is read as part of the
// tuple, not between two, since a lone "lat" tuple is never what was meant.
func coordinates(s string, abs bool) ([]fix, error) {
	var fixes []fix
	for _, tuple := range strings.Fields(commaSpace.ReplaceAllString(s, ",")) {
		f, err := kmlPoint(strings.Split(tuple, ","), ",", abs)
		if err != nil {
			return nil, err
		}
		fixes = append(fixes, f)
	}
	return fixes, nil
}

// kmlPoint reads one position, longitude first as KML writes it.
func kmlPoint(parts []string, sep string, abs bool) (fix, error) {
	var f fix
	if len(parts) < 2 || len(parts) > 3 {
		return f, fmt.Errorf("%q is not a KML coordinate", strings.Join(parts, sep))
	}
	lat, lon, err := position(parts[1], parts[0])
	if err != nil {
		return f, err
	}
	f.Lat, f.Lon, f.HasGPS = lat, lon, true
	if len(parts) == 3 && abs {
		if f.Elevation, f.HasElevation, err = number(parts[2], "altitude"); err != nil {
			return f, err
		}
	}
	return f, nil
}

// fr24Number and fr24Callsign are a flight number, "TG950", and a callsign,
// "THA950", as a Flightradar24 KML names its document: "TG950/THA950".
var (
	fr24Name = regexp.MustCompile(`^([A-Z0-9]{2}[0-9]{1,4}[A-Z]?)/([A-Z]{3}[0-9A-Z]{1,5})$`)
	iataCode = regexp.MustCompile(`\b[A-Z]{3}\b`)
	htmlTag  = regexp.MustCompile(`<[^>]*>`)
	spaces   = regexp.MustCompile(`\s+`)
)

// fr24KMLFlight is the flight a Flightradar24 KML document is of, from its name
// -- "TG950/THA950", the number and the callsign -- and, where its
// description says, the airports it flew between: the description is the
// HTML of a pop-up, "BKK Bangkok CPH Copenhagen STD 18:10 UTC ...", read
// only for the last two codes that come before the scheduled time. A document
// whose name is not a flight's is not one, whatever its description.
func fr24KMLFlight(name, description string) (Flight, bool) {
	m := fr24Name.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return Flight{}, false
	}
	f := Flight{Number: m[1], Callsign: m[2]}
	text := spaces.ReplaceAllString(html.UnescapeString(htmlTag.ReplaceAllString(description, " ")), " ")
	// The last two codes before the scheduled time: the description also
	// credits the aircraft's photographer, whose name may be a code too --
	// "© NRT Spotter" -- and comes first.
	if before, _, ok := strings.Cut(text, " STD "); ok {
		if codes := iataCode.FindAllString(before, -1); len(codes) >= 2 {
			f.From, f.To = codes[len(codes)-2], codes[len(codes)-1]
		}
	}
	return f, true
}
