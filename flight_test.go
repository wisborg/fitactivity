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
// the airports, after a photographer's credit that may look like one.
func TestReadKMLFlight(t *testing.T) {
	kml := `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2"><Document>
<name>XY123/XYZ123</name>
<description><![CDATA[<div><span>XY123 / XYZ123</span> Example Air &copy; ABC Spotter <b>AAA</b> Alphaville <b>BBB</b> Betatown <span>STD</span> 04:35 UTC</div>]]></description>
<Placemark><TimeStamp><when>2026-01-01T00:00:00Z</when></TimeStamp><Point><coordinates>10,20,0</coordinates></Point></Placemark>
<Placemark><TimeStamp><when>2026-01-01T00:01:00Z</when></TimeStamp><Point><coordinates>10.1,20,100</coordinates></Point></Placemark>
</Document></kml>`
	tr := readTrack(t, writeCourse(t, "flight.kml", kml))
	want := Flight{Number: "XY123", Callsign: "XYZ123", From: "AAA", To: "BBB"}
	if len(tr.Flights) != 1 || tr.Flights[0] != want {
		t.Errorf("flights %+v, want %+v", tr.Flights, want)
	}
	if f, ok := fr24KMLFlight("Morning run", "AAA BBB STD"); ok {
		t.Errorf("a document not named as a flight read as %+v", f)
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
