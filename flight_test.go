package fitactivity

import (
	"math"
	"testing"
	"time"
)

// A Flightradar24 CSV's direction is each sample's course, and its callsign
// the track's flight.
func TestReadFR24CourseAndCallsign(t *testing.T) {
	tr := readTrack(t, writeCourse(t, "flight.csv", fr24Flight))
	if len(tr.Flights) != 1 || tr.Flights[0].Callsign != "XYZ123" || tr.Flights[0].Number != "" {
		t.Errorf("flights %+v, want the callsign alone", tr.Flights)
	}
	if s := tr.Samples[4]; !s.HasCourse || s.Course != 191 {
		t.Errorf("course %v (has %v), want 191", s.Course, s.HasCourse)
	}
}

// A Flightradar24 KML names its flight in its document; its description has
// the airports, after a photographer's credit that may look like one, and
// the aircraft that flew it.
func TestReadKMLFlight(t *testing.T) {
	kml := `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2"><Document>
<name>XY123/XYZ123</name>
<description><![CDATA[<div><span>XY123 / XYZ123</span> Example Air &copy; ABC Spotter <b>AAA</b> Alphaville <b>BBB</b> Betatown <span>STD</span> 04:35 UTC
<td>Aircraft<span>(A20N)</span><br>
<span>Example 320-271N</span></td><td>Registration<br>
<span><a href="#">XX-ABC</a></span></td></div>]]></description>
<Placemark><TimeStamp><when>2026-01-01T00:00:00Z</when></TimeStamp><Point><coordinates>10,20,0</coordinates></Point></Placemark>
<Placemark><TimeStamp><when>2026-01-01T00:01:00Z</when></TimeStamp><Point><coordinates>10.1,20,100</coordinates></Point></Placemark>
</Document></kml>`
	tr := readTrack(t, writeCourse(t, "flight.kml", kml))
	want := Flight{Number: "XY123", Callsign: "XYZ123", From: "AAA", To: "BBB",
		Aircraft: "Example 320-271N", AircraftType: "A20N", Registration: "XX-ABC"}
	if len(tr.Flights) != 1 || tr.Flights[0] != want {
		t.Errorf("flights %+v, want %+v", tr.Flights, want)
	}
	if f, ok := fr24KMLFlight("Morning run", "AAA BBB STD"); ok {
		t.Errorf("a document not named as a flight read as %+v", f)
	}
}

// An aircraft Flightradar24 does not know is no aircraft, and one whose
// type alone it does not know is still its model; a pop-up without the
// aircraft's fields has none of them.
func TestKMLFlightAircraftWhereKnown(t *testing.T) {
	for _, c := range []struct {
		description string
		want        Flight
	}{
		{"AAA BBB STD Aircraft N/A Registration N/A", Flight{From: "AAA", To: "BBB"}},
		{"AAA BBB STD Aircraft Example 320 Registration XX-ABC", Flight{From: "AAA", To: "BBB", Aircraft: "Example 320", Registration: "XX-ABC"}},
		{"AAA BBB STD Aircraft (A20N) Example 320", Flight{From: "AAA", To: "BBB", Aircraft: "Example 320", AircraftType: "A20N"}},
		{"AAA BBB STD 04:35 UTC", Flight{From: "AAA", To: "BBB"}},
	} {
		f, ok := fr24KMLFlight("XY123/XYZ123", c.description)
		c.want.Number, c.want.Callsign = "XY123", "XYZ123"
		if !ok || f != c.want {
			t.Errorf("%q read as %+v, want %+v", c.description, f, c.want)
		}
	}
}

// A course between two samples turns the short way round.
func TestCourseInterpolatesTheShortWay(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tr := &Track{Samples: []Sample{
		{Time: t0, HasCourse: true, Course: 350},
		{Time: t0.Add(2 * time.Second), HasCourse: true, Course: 10},
	}}
	s, ok := tr.At(t0.Add(time.Second))
	if !ok || !s.HasCourse || math.Abs(s.Course-0) > 1e-9 && math.Abs(s.Course-360) > 1e-9 {
		t.Errorf("course halfway from 350 to 10 is %v (has %v), want 0", s.Course, s.HasCourse)
	}
}

// A merge keeps each file's flights, in order.
func TestMergeKeepsFlights(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &Track{SourcePath: "a", Sources: []string{"a"}, Flights: []Flight{{Number: "XY1"}}, Samples: []Sample{{Time: t0}, {Time: t0.Add(time.Minute)}}}
	b := &Track{SourcePath: "b", Sources: []string{"b"}, Flights: []Flight{{Number: "XY2"}}, Samples: []Sample{{Time: t0.Add(time.Hour)}, {Time: t0.Add(61 * time.Minute)}}}
	m, err := Merge(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Flights) != 2 || m.Flights[0].Number != "XY1" || m.Flights[1].Number != "XY2" {
		t.Errorf("merged flights %+v", m.Flights)
	}
}

// A Flightradar24 KML's speed and track are in each point's pop-up, and
// are read from there: the KML alone is the whole flight. A description
// that is not one -- Garmin Connect's laps, in km/h -- gives nothing, and a
// point whose pop-up has no speed has none.
func TestReadKMLFlightSpeedAndTrack(t *testing.T) {
	popup := func(alt, speed, heading string) string {
		return `<description><![CDATA[<div><div><span><b>Altitude:</b></span> <span>` + alt + ` ft</span></div>
<div><span><b>Speed:</b></span> <span>` + speed + ` kt</span></div>
<div><span><b>Heading:</b></span> <span>` + heading + `&deg;</span></div>
</div>]]></description>`
	}
	kml := `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2"><Document>
<name>XY123/XYZ123</name>
<Placemark>` + popup("0", "0", "14") + `<TimeStamp><when>2026-01-01T00:00:00Z</when></TimeStamp><Point><altitudeMode>absolute</altitudeMode><coordinates>10,20,0</coordinates></Point></Placemark>
<Placemark>` + popup("36,000", "1,048", "360") + `<TimeStamp><when>2026-01-01T00:01:00Z</when></TimeStamp><Point><altitudeMode>absolute</altitudeMode><coordinates>10.1,20,10972.8</coordinates></Point></Placemark>
<Placemark><description>&lt;table&gt;&lt;tr&gt;&lt;td&gt;Max Speed:&lt;/td&gt;&lt;td&gt;12.34 Kilometers per hour&lt;/td&gt;&lt;/tr&gt;&lt;/table&gt;</description><TimeStamp><when>2026-01-01T00:02:00Z</when></TimeStamp><Point><coordinates>10.2,20,0</coordinates></Point></Placemark>
</Document></kml>`
	tr := readTrack(t, writeCourse(t, "flight.kml", kml))
	if tr.Sport != "flying" {
		t.Errorf("sport %q, want flying", tr.Sport)
	}
	if len(tr.Samples) != 3 {
		t.Fatalf("%d samples, want 3", len(tr.Samples))
	}
	s0, s1, s2 := tr.Samples[0], tr.Samples[1], tr.Samples[2]
	if !s0.HasSpeed || s0.Speed != 0 || !s0.HasCourse || s0.Course != 14 {
		t.Errorf("at the gate: speed %v (has %v), course %v (has %v); want 0 and 14", s0.Speed, s0.HasSpeed, s0.Course, s0.HasCourse)
	}
	if !s1.HasSpeed || math.Abs(s1.Speed-1048*knots) > 1e-9 || !s1.HasCourse || s1.Course != 0 {
		t.Errorf("at cruise: speed %v (has %v), course %v (has %v); want %v and 0", s1.Speed, s1.HasSpeed, s1.Course, s1.HasCourse, 1048*knots)
	}
	if !s1.HasElevation || math.Abs(s1.Elevation-10972.8) > 1e-9 {
		t.Errorf("altitude %v (has %v), want 10972.8 from the coordinate", s1.Elevation, s1.HasElevation)
	}
	if s2.HasSpeed || s2.HasCourse {
		t.Errorf("a description in km/h read as speed %v (has %v), course %v (has %v)", s2.Speed, s2.HasSpeed, s2.Course, s2.HasCourse)
	}
}
