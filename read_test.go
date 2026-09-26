package fitactivity

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wisborg/fitactivity/fittest"
)

// Every course here is invented: a few points near 10°N 20°E, a place chosen
// for being nowhere in particular, at a date that is nobody's run.

var readBase = time.Date(2026, 4, 2, 6, 0, 0, 0, time.UTC)

// writeCourse writes body under name in a temp dir. The names deliberately
// carry the wrong extension wherever a test is about content, not naming.
func writeCourse(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTrack(t *testing.T, path string) *Track {
	t.Helper()
	tr, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return tr
}

const gpxRun = `<?xml version="1.0" encoding="UTF-8"?>
<gpx version="1.1" creator="test" xmlns="http://www.topografix.com/GPX/1/1"
  xmlns:tp="http://www.garmin.com/xmlschemas/TrackPointExtension/v1">
  <trk><type>running</type>
    <trkseg>
      <trkpt lat="10.0000" lon="20.0000"><ele>12.5</ele><time>2026-04-02T06:00:00Z</time>
        <extensions><tp:TrackPointExtension><tp:hr>140</tp:hr><tp:cad>85</tp:cad><tp:atemp>-3</tp:atemp></tp:TrackPointExtension><power>250</power></extensions>
      </trkpt>
      <trkpt lat="10.0002" lon="20.0000"><time>2026-04-02T08:00:02+02:00</time></trkpt>
    </trkseg>
    <trkseg>
      <trkpt lat="10.0001" lon="20.0000"><ele>13</ele><time>2026-04-02T06:00:01.500Z</time>
        <extensions><tp:TrackPointExtension><tp:hr>141</tp:hr></tp:TrackPointExtension></extensions>
      </trkpt>
    </trkseg>
  </trk>
</gpx>`

// A GPX track is read in time order across its segments, with every field
// the point carries -- the Garmin extension's in its own namespace, and a
// bare power -- and nothing it does not: the second point has no heart rate,
// and must not borrow the first one's or read as zero.
func TestReadGPX(t *testing.T) {
	tr := readTrack(t, writeCourse(t, "run.dat", gpxRun))
	if len(tr.Samples) != 3 {
		t.Fatalf("%d samples, want 3", len(tr.Samples))
	}
	if tr.Sport != "running" {
		t.Errorf("sport %q", tr.Sport)
	}
	s0, s1, s2 := tr.Samples[0], tr.Samples[1], tr.Samples[2]
	if !s0.Time.Equal(readBase) || !s1.Time.Equal(readBase.Add(1500*time.Millisecond)) || !s2.Time.Equal(readBase.Add(2*time.Second)) {
		t.Errorf("times %v %v %v; want sorted, with the +02:00 one at 06:00:02 UTC", s0.Time, s1.Time, s2.Time)
	}
	if !s0.HasGPS || s0.Lat != 10 || s0.Lon != 20 || !s0.HasElevation || s0.Elevation != 12.5 {
		t.Errorf("first point %+v", s0)
	}
	if !s0.HasHeartRate || s0.HeartRate != 140 || !s0.HasCadence || s0.Cadence != 85 ||
		!s0.HasTemperature || s0.Temperature != -3 || !s0.HasPower || s0.Power != 250 {
		t.Errorf("first point's extensions %+v", s0)
	}
	if s2.HasHeartRate || s2.HasElevation || s2.HasCadence || s2.HasPower || s2.HasTemperature {
		t.Errorf("a point with only a position and a time reads as %+v", s2)
	}
	if s0.HasDistance || s0.HasSpeed {
		t.Error("distance or speed read from a file that carries neither")
	}
}

// A file without a time on every point is not a recording. Read says so,
// with ErrNoTimes, and ReadRoute reads its geometry in the file's order.
func TestReadRefusesACourseWithoutTimes(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a GPX track with no times", `<gpx><trk><trkseg>
			<trkpt lat="10.0" lon="20.0"/><trkpt lat="10.2" lon="20.0"/><trkpt lat="10.1" lon="20.0"/>
			</trkseg></trk></gpx>`},
		{"a GPX track timed in part", `<gpx><trk><trkseg>
			<trkpt lat="10.0" lon="20.0"><time>2026-04-02T06:00:00Z</time></trkpt>
			<trkpt lat="10.2" lon="20.0"/><trkpt lat="10.1" lon="20.0"/>
			</trkseg></trk></gpx>`},
		{"a GPX route, times and all", `<gpx><rte>
			<rtept lat="10.0" lon="20.0"><time>2026-04-02T06:00:02Z</time></rtept>
			<rtept lat="10.2" lon="20.0"><time>2026-04-02T06:00:01Z</time></rtept>
			<rtept lat="10.1" lon="20.0"><time>2026-04-02T06:00:00Z</time></rtept>
			</rte></gpx>`},
		{"a KML line", `<kml xmlns="http://www.opengis.net/kml/2.2"><Document><Placemark>
			<TimeSpan><begin>2026-04-02T06:00:00Z</begin><end>2026-04-02T07:00:00Z</end></TimeSpan>
			<LineString><coordinates>20.0,10.0 20.0,10.2 20.0,10.1</coordinates></LineString>
			</Placemark></Document></kml>`},
		{"a TCX course", `<TrainingCenterDatabase><Courses><Course><Track>
			<Trackpoint><Time>2026-04-02T06:00:00Z</Time><Position><LatitudeDegrees>10.0</LatitudeDegrees><LongitudeDegrees>20.0</LongitudeDegrees></Position></Trackpoint>
			<Trackpoint><Time>2026-04-02T06:00:01Z</Time><Position><LatitudeDegrees>10.2</LatitudeDegrees><LongitudeDegrees>20.0</LongitudeDegrees></Position></Trackpoint>
			<Trackpoint><Time>2026-04-02T06:00:02Z</Time><Position><LatitudeDegrees>10.1</LatitudeDegrees><LongitudeDegrees>20.0</LongitudeDegrees></Position></Trackpoint>
			</Track></Course></Courses></TrainingCenterDatabase>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeCourse(t, "course.dat", tc.body)
			if tr, err := Read(path); !errors.Is(err, ErrNoTimes) {
				t.Fatalf("Read gave %v, %v; want ErrNoTimes", tr, err)
			}
			r, err := ReadRoute(path)
			if err != nil {
				t.Fatal(err)
			}
			var lats []float64
			for _, p := range r.Points {
				lats = append(lats, p.Lat)
			}
			if len(lats) != 3 || lats[0] != 10.0 || lats[1] != 10.2 || lats[2] != 10.1 {
				t.Errorf("route latitudes %v; want the file's order, 10 10.2 10.1", lats)
			}
		})
	}
}

// A timed file's route is its track's geometry, in time order.
func TestReadRouteOfARecordingIsInTimeOrder(t *testing.T) {
	r, err := ReadRoute(writeCourse(t, "run.dat", gpxRun))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Points) != 3 || r.Points[1].Lat != 10.0001 || r.Sport != "running" {
		t.Errorf("route %+v", r)
	}
	if !r.Points[0].HasElevation || r.Points[2].HasElevation {
		t.Error("route elevation presence does not follow the points'")
	}
}

const tcxRun = `<?xml version="1.0"?>
<TrainingCenterDatabase xmlns="http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2"
  xmlns:ns3="http://www.garmin.com/xmlschemas/ActivityExtension/v2">
  <Activities><Activity Sport="Biking">
    <Lap StartTime="2026-04-02T06:00:01Z"><Track>
      <Trackpoint><Time>2026-04-02T06:00:01Z</Time>
        <Position><LatitudeDegrees>10.0001</LatitudeDegrees><LongitudeDegrees>20</LongitudeDegrees></Position>
        <AltitudeMeters>5</AltitudeMeters><DistanceMeters>11.1</DistanceMeters>
        <HeartRateBpm><Value>120</Value></HeartRateBpm><Cadence>90</Cadence>
        <Extensions><ns3:TPX><ns3:Speed>4.5</ns3:Speed><ns3:Watts>210</ns3:Watts></ns3:TPX></Extensions>
      </Trackpoint>
    </Track></Lap>
    <Lap StartTime="2026-04-02T06:00:00Z"><Track>
      <Trackpoint><Time>2026-04-02T06:00:00Z</Time><DistanceMeters>0</DistanceMeters><HeartRateBpm><Value>118</Value></HeartRateBpm></Trackpoint>
    </Track></Lap>
    <Lap StartTime="2026-04-02T06:00:01Z"><Track>
      <Trackpoint><Time>2026-04-02T06:00:01Z</Time>
        <Position><LatitudeDegrees>10.0001</LatitudeDegrees><LongitudeDegrees>20</LongitudeDegrees></Position>
        <AltitudeMeters>5</AltitudeMeters><DistanceMeters>11.1</DistanceMeters>
        <HeartRateBpm><Value>120</Value></HeartRateBpm><Cadence>90</Cadence>
        <Extensions><ns3:TPX><ns3:Speed>4.5</ns3:Speed><ns3:Watts>210</ns3:Watts></ns3:TPX></Extensions>
      </Trackpoint>
      <Trackpoint><Time>2026-04-02T06:00:02Z</Time>
        <Position><LatitudeDegrees>10.0002</LatitudeDegrees><LongitudeDegrees>20</LongitudeDegrees></Position>
        <DistanceMeters>22.2</DistanceMeters><Extensions><ns3:TPX><ns3:RunCadence>80</ns3:RunCadence></ns3:TPX></Extensions>
      </Trackpoint>
    </Track></Lap>
  </Activity></Activities>
</TrainingCenterDatabase>`

// A TCX activity's trackpoints carry distance as FIT's do, and its
// ActivityExtension's speed and power. A point with no position is kept as a
// reading with no fix. A lap that repeats the point the last one ended on
// yields it once, and the activity starts at its earliest lap.
func TestReadTCX(t *testing.T) {
	tr := readTrack(t, writeCourse(t, "ride.dat", tcxRun))
	if len(tr.Samples) != 3 {
		t.Fatalf("%d samples, want 3 with the repeated one once", len(tr.Samples))
	}
	if tr.Sport != "cycling" {
		t.Errorf("sport %q; TCX's Biking is FIT's cycling", tr.Sport)
	}
	if !tr.Timing.Start.Equal(readBase) || tr.Timing.HasTotals {
		t.Errorf("timing %+v; want the earliest lap's start and no totals", tr.Timing)
	}
	s0, s1, s2 := tr.Samples[0], tr.Samples[1], tr.Samples[2]
	if s0.HasGPS || s0.HasElevation || !s0.HasHeartRate || s0.HeartRate != 118 || !s0.HasDistance {
		t.Errorf("the point with no position reads as %+v", s0)
	}
	if !s1.HasGPS || s1.Lat != 10.0001 || s1.Elevation != 5 || s1.Distance != 11.1 || s1.HeartRate != 120 ||
		s1.Cadence != 90 || !s1.HasSpeed || s1.Speed != 4.5 || !s1.HasPower || s1.Power != 210 {
		t.Errorf("the full point reads as %+v", s1)
	}
	if !s2.HasCadence || s2.Cadence != 80 || s2.HasHeartRate || s2.HasPower || s2.HasElevation {
		t.Errorf("the last point reads as %+v", s2)
	}
}

const kmlRun = `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://earth.google.com/kml/2.1">
  <Folder>
    <Placemark><LineString><coordinates>21,11 21,12</coordinates></LineString></Placemark>
    <Folder>
      <Placemark><TimeSpan><begin>2026-04-02T06:00:01Z</begin><end>2026-04-02T06:00:02Z</end></TimeSpan>
        <Point><altitudeMode>absolute</altitudeMode><coordinates>20.0, 10.0002, 30</coordinates></Point></Placemark>
      <Placemark><TimeSpan><begin>2026-04-02T06:00:00Z</begin><end>2026-04-02T06:00:00Z</end></TimeSpan>
        <Point><altitudeMode>clampToGround</altitudeMode><coordinates>20.0,10.0,30</coordinates></Point></Placemark>
      <Placemark><TimeStamp><when>2026-04-02T16:00:01+10:00</when></TimeStamp>
        <Point><altitudeMode>absolute</altitudeMode><coordinates>20.0,10.0001</coordinates></Point></Placemark>
      <Placemark><name>Start</name><Point><coordinates>22,13</coordinates></Point></Placemark>
    </Folder>
  </Folder>
</kml>`

// KML's timed Points are the course, and the line beside them and the
// untimed marker are drawings over it. A TimeSpan's time is its end. An
// altitude is an elevation only where the altitudeMode is absolute -- not
// clamped to the ground, and not a point with no altitude at all -- and
// whitespace after a comma is part of a coordinate.
func TestReadKMLPoints(t *testing.T) {
	tr := readTrack(t, writeCourse(t, "run.dat", kmlRun))
	if len(tr.Samples) != 3 {
		t.Fatalf("%d samples, want the 3 timed points", len(tr.Samples))
	}
	for i, want := range []struct {
		lat    float64
		hasEle bool
	}{{10.0, false}, {10.0001, false}, {10.0002, true}} {
		s := tr.Samples[i]
		if !s.Time.Equal(readBase.Add(time.Duration(i)*time.Second)) || s.Lat != want.lat || s.Lon != 20 {
			t.Errorf("sample %d at %v is %v,%v", i, s.Time, s.Lat, s.Lon)
		}
		if s.HasElevation != want.hasEle || (want.hasEle && s.Elevation != 30) {
			t.Errorf("sample %d elevation %v %v; want present %v", i, s.HasElevation, s.Elevation, want.hasEle)
		}
	}
}

// A gx:Track pairs each when with its coord, in a MultiTrack whose
// altitudeMode reaches the tracks inside it.
func TestReadKMLGxTrack(t *testing.T) {
	body := `<kml xmlns="http://www.opengis.net/kml/2.2" xmlns:gx="http://www.google.com/kml/ext/2.2">
		<Document><Placemark><gx:MultiTrack><altitudeMode>absolute</altitudeMode><gx:Track>
		<when>2026-04-02T06:00:00Z</when><when>2026-04-02T06:00:01Z</when>
		<gx:coord>20 10 7</gx:coord><gx:coord>20 10.0001 8</gx:coord>
		</gx:Track></gx:MultiTrack></Placemark></Document></kml>`
	tr := readTrack(t, writeCourse(t, "t.dat", body))
	if len(tr.Samples) != 2 || tr.Samples[1].Lat != 10.0001 || !tr.Samples[1].HasElevation || tr.Samples[1].Elevation != 8 {
		t.Errorf("samples %+v", tr.Samples)
	}

	bad := strings.Replace(body, "<when>2026-04-02T06:00:01Z</when>", "", 1)
	if _, err := Read(writeCourse(t, "t.dat", bad)); err == nil || errors.Is(err, ErrNoTimes) {
		t.Errorf("a gx:Track with a time missing read as %v; want an error that is not ErrNoTimes", err)
	}
}

// A KMZ is read by the KML at its root, not one filed deeper in it.
func TestReadKMZ(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.dat")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, e := range []struct{ name, body string }{
		{"files/other.kml", `<kml><Placemark><LineString><coordinates>1,1 2,2</coordinates></LineString></Placemark></kml>`},
		{"doc.kml", kmlRun},
	} {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if tr := readTrack(t, path); len(tr.Samples) != 3 {
		t.Errorf("%d samples; the KMZ was not read by its root KML", len(tr.Samples))
	}
}

// The format is told by content: a FIT file named .gpx is still FIT, and
// what is none of them is refused rather than read as an empty course.
func TestReadTellsFormatsByContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "misnamed.gpx")
	opts := fittest.DefaultOptions()
	opts.Count = 30
	if err := fittest.WriteFile(path, opts); err != nil {
		t.Fatal(err)
	}
	fromRead := readTrack(t, path)
	fromDecode, err := Decode(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromRead.Samples) != len(fromDecode.Samples) || fromRead.Sport != fromDecode.Sport {
		t.Error("Read of a FIT file is not Decode's")
	}
	r, err := ReadRoute(path)
	if err != nil {
		t.Fatal(err)
	}
	gps := 0
	for _, s := range fromDecode.Samples {
		if s.HasGPS {
			gps++
		}
	}
	if len(r.Points) != gps {
		t.Errorf("route of %d points from %d fixes", len(r.Points), gps)
	}

	for _, body := range []string{"not a course", `<svg xmlns="http://www.w3.org/2000/svg"/>`, ""} {
		if _, err := Read(writeCourse(t, "x.gpx", body)); err == nil {
			t.Errorf("%q was read as a course", body)
		}
	}
}

// What a file says wrongly is an error, not a value bent to fit: a position
// off the globe, a heart rate a byte cannot hold, a time with no zone.
func TestReadRefusesImpossibleValues(t *testing.T) {
	for name, pt := range map[string]string{
		"latitude":  `<trkpt lat="91" lon="20"><time>2026-04-02T06:00:00Z</time></trkpt>`,
		"heartrate": `<trkpt lat="10" lon="20"><time>2026-04-02T06:00:00Z</time><extensions><hr>300</hr></extensions></trkpt>`,
		"zoneless":  `<trkpt lat="10" lon="20"><time>2026-04-02T06:00:00</time></trkpt>`,
		"elevation": `<trkpt lat="10" lon="20"><ele>high</ele><time>2026-04-02T06:00:00Z</time></trkpt>`,
	} {
		body := `<gpx><trk><trkseg>` + pt + `</trkseg></trk></gpx>`
		if _, err := Read(writeCourse(t, "x.gpx", body)); err == nil || errors.Is(err, ErrNoTimes) {
			t.Errorf("%s: read as %v", name, err)
		}
	}
}

// DecodeAll merges files of different formats as it merges FIT files.
func TestDecodeAllMergesFormats(t *testing.T) {
	dir := t.TempDir()
	fitPath := filepath.Join(dir, "later.fit")
	opts := fittest.DefaultOptions()
	opts.Start = readBase.Add(time.Hour)
	opts.Count = 30
	if err := fittest.WriteFile(fitPath, opts); err != nil {
		t.Fatal(err)
	}
	gpxPath := writeCourse(t, "earlier.gpx", gpxRun)
	merged, err := DecodeAll(fitPath, gpxPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Samples) != 33 || !merged.Samples[0].Time.Equal(readBase) {
		t.Errorf("%d samples from %v; want the GPX's 3 first, then the FIT's 30", len(merged.Samples), merged.Samples[0].Time)
	}
}

// Zero is a coordinate like any other: a fix on the equator or the prime
// meridian is a fix, in every format.
func TestReadKeepsAPositionAtZero(t *testing.T) {
	for name, body := range map[string]string{
		"gpx": `<gpx><trk><trkseg><trkpt lat="0" lon="0"><time>2026-04-02T06:00:00Z</time></trkpt></trkseg></trk></gpx>`,
		"tcx": `<TrainingCenterDatabase><Activities><Activity><Lap><Track><Trackpoint><Time>2026-04-02T06:00:00Z</Time>
			<Position><LatitudeDegrees>0</LatitudeDegrees><LongitudeDegrees>0</LongitudeDegrees></Position></Trackpoint></Track></Lap></Activity></Activities></TrainingCenterDatabase>`,
		"kml": `<kml><Placemark><TimeStamp><when>2026-04-02T06:00:00Z</when></TimeStamp><Point><coordinates>0,0</coordinates></Point></Placemark></kml>`,
	} {
		tr := readTrack(t, writeCourse(t, "zero."+name, body))
		if len(tr.Samples) != 1 || !tr.Samples[0].HasGPS {
			t.Errorf("%s: a fix at 0,0 reads as %+v", name, tr.Samples)
		}
	}
}

// A TCX trackpoint's own heart rate is the one read, ahead of any an
// extension also carries: the schema's field is the device's reading, and an
// extension is whatever some other program added.
func TestReadTCXPrefersItsOwnHeartRate(t *testing.T) {
	body := `<TrainingCenterDatabase><Activities><Activity><Lap><Track><Trackpoint><Time>2026-04-02T06:00:00Z</Time>
		<HeartRateBpm><Value>130</Value></HeartRateBpm><Extensions><x:TPX xmlns:x="urn:x"><x:hr>99</x:hr></x:TPX></Extensions>
		</Trackpoint></Track></Lap></Activity></Activities></TrainingCenterDatabase>`
	tr := readTrack(t, writeCourse(t, "hr.tcx", body))
	if s := tr.Samples[0]; !s.HasHeartRate || s.HeartRate != 130 {
		t.Errorf("heart rate %v %d; want the trackpoint's 130", s.HasHeartRate, s.HeartRate)
	}
}
