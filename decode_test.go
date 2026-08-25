package fitactivity

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/muktihari/fit/profile/basetype"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/proto"
)

// realFITEnv names the environment variable pointing at a real Garmin
// recording for TestDecode_RealFile:
//
//	FITACTIVITY_REAL_FIT="$HOME/activities/run.fit" go test github.com/wisborg/fitactivity
//
// Use an ABSOLUTE path: go test runs each package with its own directory as
// the working directory, so a repo-relative one resolves somewhere unexpected
// and silently fails to open.
//
// It is an env var rather than a checked-in path because a FIT file from a
// watch is personal data -- where someone was, minute by minute, and what
// their heart was doing -- so no recording is distributed with this library,
// and hard-coding a filename would only advertise a file nobody else has.
// Same convention videofx uses for its own footage-gated probe tests.
const realFITEnv = "FITACTIVITY_REAL_FIT"

// TestDecode_RealFile checks Decode against a genuine device recording, by
// asserting PROPERTIES that any real Garmin activity satisfies rather than
// pinning the values of one particular file.
//
// # Why a real file at all
//
// Everyday coverage comes from fittest's generated activities, which every
// other test here uses and which need no environment at all. What only a real
// device file can prove is that Decode still copes with what Garmin actually
// writes: developer-field registrations from a real sensor, the invalid
// sentinels a real recording carries, and whatever message ordering the device
// chose. A file this library wrote cannot vouch for any of that.
//
// # Why properties rather than pinned values
//
// An earlier version pinned one recording's record count, coverage window,
// mid-sample coordinate, distance, speed and temperature. It was a sharper
// regression test for its author and a worse test for everyone else: only the
// person holding that file could run it, and the numbers it pinned were a
// detailed record of where a specific person was on a specific evening --
// published in a library other people pull.
//
// The properties below give up catching a value-level drift on one specific
// file. In exchange they run against ANY real recording, so a contributor can
// point this at their own activity and have it mean something. Where a pinned
// value was really guarding against a scale or sentinel error, that is
// expressed here as a plausibility bound instead, which catches the
// order-of-magnitude mistakes that actually happen -- a semicircle conversion
// dropped, a scale divisor missed -- without recording anybody's data.
//
// Physiological values were never pinned here and still are not. The rule is
// unchanged: this test pins structure and mechanism, not body measurements.
func TestDecode_RealFile(t *testing.T) {
	realTestFIT := os.Getenv(realFITEnv)
	if realTestFIT == "" {
		t.Skipf("%s not set; see this test's doc comment", realFITEnv)
	}
	if _, err := os.Stat(realTestFIT); err != nil {
		t.Fatalf("%s=%q is not readable: %v", realFITEnv, realTestFIT, err)
	}

	track, err := Decode(realTestFIT)
	if err != nil {
		t.Fatalf("Decode(%q) returned error: %v", realTestFIT, err)
	}
	if track.Len() < 2 {
		t.Fatalf("Len() = %d; a real activity should carry many records", track.Len())
	}
	// Not an assertion: a reader debugging a failure below wants to know what
	// the file was, and the record count and sport are the cheapest orienting
	// facts that are not personal. An empty sport is legitimate -- some
	// devices omit it.
	t.Logf("decoded %d records, sport=%q", track.Len(), track.Sport)

	// Decode sorts explicitly (see decode.go) so every later phase can rely on
	// the ordering without re-checking it. Nothing in the FIT format
	// guarantees a device wrote the records in order, so this is a real
	// contract -- though note it can only FAIL on a file that is actually out
	// of order, which most are not. TestDecode_SortsOutOfOrderRecords is what
	// covers the sort itself.
	for i := 1; i < track.Len(); i++ {
		if track.Samples[i].Time.Before(track.Samples[i-1].Time) {
			t.Fatalf("samples not sorted: Samples[%d].Time is before Samples[%d].Time", i, i-1)
		}
	}

	// Coverage must agree with the slice it summarises. Two ways of asking the
	// same question; a disagreement means one is reading the wrong end.
	first, last := track.Coverage()
	if !first.Equal(track.Samples[0].Time) {
		t.Error("Coverage first disagrees with Samples[0].Time")
	}
	if !last.Equal(track.Samples[track.Len()-1].Time) {
		t.Error("Coverage last disagrees with the final sample's Time")
	}

	if fault := gpsFault(track.Samples); fault != "" {
		t.Error(fault)
	}
	if fault := scaleFault(track.Samples); fault != "" {
		t.Error(fault)
	}
	assertDistanceMatchesSpeed(t, track)
	assertDevFieldsResolved(t, track)
}

// gpsFault returns a description of the first sample claiming a fix it cannot
// have, or "" when every fix is plausible.
//
// It is a pure function, separate from the test that calls it, because the
// assertion is unfalsifiable on a good recording: a file whose GPS never
// dropped out contains no sentinel to leak, so a broken sentinel check and a
// working one produce identical results on it. That was measured, not assumed
// -- letting the sentinel through unconditionally in sampleFromRecord still
// passed against a real activity carrying a fix on all 1554 of its samples.
// Split out, the rule gets its own test against a deliberately poisoned slice
// (TestGPSFault_CatchesASentinelDecodedAsAFix), so the real-file check is a
// net over whatever file a contributor supplies while the rule itself is
// genuinely covered.
//
// FIT stores position in semicircles and marks "no fix" with an out-of-range
// sentinel. A decoder that lets it through does not produce garbage that looks
// like garbage -- it produces a coordinate near the pole, which is a perfectly
// well-formed latitude. The 0,0 case is the same absence resolved to zero
// instead: a valid coordinate that passes every range check, wrong only
// because no activity is recorded in the Gulf of Guinea.
func gpsFault(samples []Sample) string {
	for i, s := range samples {
		if !s.HasGPS {
			continue
		}
		if s.Lat < -90 || s.Lat > 90 {
			return fmt.Sprintf("Samples[%d] has HasGPS with latitude out of range; a sentinel was decoded as a fix", i)
		}
		if s.Lon < -180 || s.Lon > 180 {
			return fmt.Sprintf("Samples[%d] has HasGPS with longitude out of range; a sentinel was decoded as a fix", i)
		}
		if s.Lat == 0 && s.Lon == 0 {
			return fmt.Sprintf("Samples[%d] has HasGPS at exactly 0,0 -- absence decoded as a position", i)
		}
	}
	return ""
}

// scaleFault returns a description of the first sample whose value falls
// outside the range its unit permits, or "" when every field is plausible.
//
// This is what the pinned distance, speed, elevation and temperature values
// were really guarding: that each field came out in the units this library
// documents. A dropped scale divisor is an order-of-magnitude error, not a
// subtle one -- speed in mm/s rather than m/s is off by a thousand -- so wide
// bounds catch every such mistake that has actually happened while pinning
// nothing about the activity. Pure and separately tested for the same reason
// as gpsFault.
func scaleFault(samples []Sample) string {
	var prevDist float64
	var haveDist bool
	for i, s := range samples {
		if s.HasDistance {
			if haveDist && s.Distance < prevDist {
				return fmt.Sprintf("Samples[%d].Distance decreases; cumulative distance must not go backwards", i)
			}
			prevDist, haveDist = s.Distance, true
		}
		if s.HasSpeed && (s.Speed < 0 || s.Speed > 50) {
			return fmt.Sprintf("Samples[%d].Speed is outside 0..50 m/s; the scale looks wrong", i)
		}
		if s.HasElevation && (s.Elevation < -500 || s.Elevation > 9000) {
			return fmt.Sprintf("Samples[%d].Elevation is outside -500..9000 m; the scale looks wrong", i)
		}
		if s.HasTemperature && (s.Temperature < -60 || s.Temperature > 70) {
			return fmt.Sprintf("Samples[%d].Temperature is outside -60..70 C; the scale looks wrong", i)
		}
		if s.HasHeartRate && (s.HeartRate < 20 || s.HeartRate > 250) {
			return fmt.Sprintf("Samples[%d].HeartRate is outside 20..250 bpm; a sentinel or scale error", i)
		}
	}
	return ""
}

// assertDistanceMatchesSpeed cross-checks two fields that are decoded through
// completely separate paths.
//
// If one gained a scale error the other would not, so integrating speed across
// the recording must land within a wide band of the distance the file reports.
// Pauses, dropouts and stopped time all push the two apart legitimately, hence
// the very loose band: this is an order-of-magnitude check, and tightening it
// would make it flaky against an activity with a lot of stopped time.
func assertDistanceMatchesSpeed(t *testing.T, track *Track) {
	t.Helper()
	var integrated float64
	for i := 1; i < track.Len(); i++ {
		prev, cur := track.Samples[i-1], track.Samples[i]
		if !prev.HasSpeed || !cur.HasSpeed {
			continue
		}
		dt := cur.Time.Sub(prev.Time).Seconds()
		if dt <= 0 || dt > 10 {
			continue // a gap; integrating across it would invent distance
		}
		integrated += (prev.Speed + cur.Speed) / 2 * dt
	}
	reported := reportedDistance(track)
	if reported <= 0 || integrated <= 0 {
		t.Log("distance/speed cross-check skipped: the file lacks one of the two")
		return
	}
	if ratio := integrated / reported; ratio < 0.5 || ratio > 2 {
		t.Errorf("integrated speed and reported distance differ by %.1fx; one of the two has a scale error", ratio)
	}
}

// reportedDistance returns the activity's total cumulative distance: the last
// present Distance minus the first. A span rather than the final value,
// because a track can legitimately begin part way through an activity, in
// which case the first sample's cumulative distance is not zero.
func reportedDistance(track *Track) float64 {
	var lo, hi float64
	var seen bool
	for _, s := range track.Samples {
		if !s.HasDistance {
			continue
		}
		if !seen {
			lo, hi, seen = s.Distance, s.Distance, true
			continue
		}
		hi = s.Distance
	}
	if !seen {
		return 0
	}
	return hi - lo
}

// assertDevFieldsResolved checks the one mechanism a generated fixture can
// only imitate: turning a real sensor's developer fields into named values
// using the FieldDescription messages that sensor wrote.
//
// Resolution is under test, not the readings. A resolved field lands under its
// human-readable name; an unresolved one falls back to a
// "<developerDataIndex>.<fieldNum>" key. So a file that registered developer
// fields but produced only fallback keys means indexFieldDescriptions or
// resolveDevField stopped working -- visible without looking at a single
// value, which is the point, because those values are body measurements.
//
// A recording with no developer fields at all is normal (no footpod, no
// third-party app) and is not a failure; it just cannot exercise this path.
func assertDevFieldsResolved(t *testing.T, track *Track) {
	t.Helper()
	named, fallback := 0, 0
	for _, s := range track.Samples {
		for k := range s.DevFields {
			if isFallbackDevFieldKey(k) {
				fallback++
			} else {
				named++
			}
		}
	}
	if named == 0 && fallback == 0 {
		t.Log("no developer fields in this recording; resolution path not exercised")
		return
	}
	if named == 0 {
		t.Errorf("all %d developer fields fell back to numeric keys; name resolution is broken", fallback)
	}
	t.Logf("developer fields: %d resolved by name, %d fell back to numeric keys", named, fallback)
}

// isFallbackDevFieldKey reports whether k is the "<devIdx>.<fieldNum>" shape
// resolveDevField uses when a file registered a developer field but supplied
// no FieldDescription naming it.
func isFallbackDevFieldKey(k string) bool {
	dot := strings.IndexByte(k, '.')
	if dot <= 0 || dot == len(k)-1 {
		return false
	}
	for i, c := range k {
		if i == dot {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// devFieldNames is a small test helper for building a readable error
// message out of a DevFields map's keys.
func devFieldNames(m map[string]float64) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	return names
}

// TestSampleFromRecord_NoGPS exercises the "no GPS fix" path that the
// real test FIT file doesn't happen to contain (that recording never
// lost its fix): a Record whose PositionLat/PositionLong are FIT's
// invalid-semicircle sentinel must decode to HasGPS=false, not a
// coordinate near the north pole, while unrelated fields (here, heart
// rate) on the same Record remain present. mesgdef.NewRecord(nil)
// starts every field at its type's invalid sentinel (see the generated
// Record.Reset for mesg==nil), which is exactly the "no data at all"
// state a real device would report before GPS lock -- this is not a
// contrived value, it's the library's own zero value for "absent".
func TestSampleFromRecord_NoGPS(t *testing.T) {
	rec := mesgdef.NewRecord(nil)
	rec.Timestamp = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	rec.HeartRate = 92

	s := sampleFromRecord(rec, nil)

	if s.HasGPS {
		t.Errorf("HasGPS = true (Lat=%v, Lon=%v), want false for an all-invalid Record", s.Lat, s.Lon)
	}
	if !s.HasHeartRate || s.HeartRate != 92 {
		t.Errorf("HeartRate = %v (present=%v), want 92 -- an absent GPS fix must not suppress unrelated present fields", s.HeartRate, s.HasHeartRate)
	}
	if s.HasElevation || s.HasSpeed || s.HasDistance || s.HasCadence || s.HasTemperature || s.HasPower {
		t.Error("expected every other field to be absent on an all-invalid Record")
	}
	if s.DevFields != nil {
		t.Errorf("DevFields = %v, want nil for a Record with no developer fields", s.DevFields)
	}
}

// TestSampleFromRecord_ValidGPS is NoGPS's counterpart: a Record with an
// explicit valid position must decode with HasGPS true and the exact
// degrees round-tripped through FIT's semicircle encoding.
func TestSampleFromRecord_ValidGPS(t *testing.T) {
	rec := mesgdef.NewRecord(nil)
	rec.Timestamp = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	rec.SetPositionLatDegrees(12.345678)
	rec.SetPositionLongDegrees(98.765432)

	s := sampleFromRecord(rec, nil)

	if !s.HasGPS {
		t.Fatal("HasGPS = false, want true for a Record with an explicit valid position")
	}
	// Semicircle encoding is lossy (32-bit fixed point), so this checks
	// a tight tolerance rather than exact equality.
	if math.Abs(s.Lat-(12.345678)) > 1e-5 {
		t.Errorf("Lat = %v, want ~12.345678", s.Lat)
	}
	if math.Abs(s.Lon-98.765432) > 1e-5 {
		t.Errorf("Lon = %v, want ~98.765432", s.Lon)
	}
}

// TestSampleFromRecord_UnresolvedDevField confirms the fallback path in
// resolveDevField: a developer field whose (developerDataIndex, num)
// pair has no matching FieldDescription (e.g. a truncated file, or one
// this test constructs by hand without registering the description) is
// still captured, under its "<devIdx>.<num>" numeric key, rather than
// silently dropped.
func TestSampleFromRecord_UnresolvedDevField(t *testing.T) {
	rec := mesgdef.NewRecord(nil)
	rec.Timestamp = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	rec.DeveloperFields = []proto.DeveloperField{
		{DeveloperDataIndex: 0, Num: 8, Value: proto.Uint16(76)},
	}

	s := sampleFromRecord(rec, devFieldIndex{}) // empty index: nothing resolves

	want := "0.8"
	got, ok := s.DevFields[want]
	if !ok {
		t.Fatalf("DevFields[%q] missing, got keys %v", want, devFieldNames(s.DevFields))
	}
	if got != 76 {
		t.Errorf("DevFields[%q] = %v, want 76", want, got)
	}
}

// TestInvalidFloat checks invalidFloat's bit-pattern comparison directly
// against basetype's own invalid float64 sentinel, plus a couple of
// values that must NOT be mistaken for it (a real NaN produced some
// other way, and an ordinary finite value).
func TestInvalidFloat(t *testing.T) {
	sentinel := math.Float64frombits(basetype.Float64Invalid)
	if !invalidFloat(sentinel) {
		t.Error("invalidFloat(basetype.Float64Invalid bit pattern) = false, want true")
	}
	if invalidFloat(math.NaN()) {
		t.Error("invalidFloat(math.NaN()) = true, want false -- a differently-bit-patterned NaN must not be mistaken for the FIT sentinel")
	}
	if invalidFloat(0) {
		t.Error("invalidFloat(0) = true, want false")
	}
	if invalidFloat(12.345678) {
		t.Error("invalidFloat(12.345678) = true, want false")
	}
}

// TestGPSFault_CatchesASentinelDecodedAsAFix is the test that makes the
// real-file GPS check worth having.
//
// It exists because of a measured failure: letting the invalid-semicircle
// sentinel through unconditionally in sampleFromRecord still passed the
// real-file test, because that recording holds a fix on every one of its 1554
// samples and so contains no sentinel to leak. A rule that cannot fire on the
// available data is not covered by running it against that data.
//
// Each case is a coordinate a real receiver cannot produce but a broken
// decoder can.
func TestGPSFault_CatchesASentinelDecodedAsAFix(t *testing.T) {
	cases := []struct {
		name    string
		sample  Sample
		wantHit bool
	}{
		{"a plausible fix", Sample{HasGPS: true, Lat: 12.345678, Lon: 98.765432}, false},
		{"sentinel decoded near the pole", Sample{HasGPS: true, Lat: 179.9, Lon: 179.9}, true},
		{"latitude below the south pole", Sample{HasGPS: true, Lat: -91, Lon: 0.5}, true},
		{"longitude past the antimeridian", Sample{HasGPS: true, Lat: 1, Lon: 180.5}, true},
		{"absence resolved to zero", Sample{HasGPS: true, Lat: 0, Lon: 0}, true},
		// The same impossible coordinates WITHOUT a claimed fix are not a
		// fault: Lat/Lon are documented as meaningless when HasGPS is false,
		// so a decoder leaving whatever was there is correct. A rule that
		// flagged these would fire on every sample of an indoor recording.
		{"impossible values but no claimed fix", Sample{HasGPS: false, Lat: 999, Lon: 999}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gpsFault([]Sample{c.sample})
			if hit := got != ""; hit != c.wantHit {
				t.Errorf("gpsFault = %q (fault=%v), want fault=%v", got, hit, c.wantHit)
			}
		})
	}
}

// TestScaleFault_CatchesUnitErrors pins the bounds against the mistakes they
// are for. Every "bad" case is an order-of-magnitude unit error rather than an
// implausible-but-possible reading, because that is what a dropped scale
// divisor produces -- and the "good" cases are deliberately extreme real data,
// so a future tightening of the bounds fails here rather than in the field.
func TestScaleFault_CatchesUnitErrors(t *testing.T) {
	cases := []struct {
		name    string
		samples []Sample
		wantHit bool
	}{
		{"ordinary running data", []Sample{
			{HasSpeed: true, Speed: 3.2, HasElevation: true, Elevation: 42,
				HasTemperature: true, Temperature: 21, HasHeartRate: true, HeartRate: 148},
		}, false},
		{"a fast cyclist is still valid", []Sample{{HasSpeed: true, Speed: 25}}, false},
		{"speed left in mm/s", []Sample{{HasSpeed: true, Speed: 3200}}, true},
		{"a Himalayan altitude is still valid", []Sample{{HasElevation: true, Elevation: 8500}}, false},
		{"elevation left in cm", []Sample{{HasElevation: true, Elevation: 420000}}, true},
		{"heart rate sentinel", []Sample{{HasHeartRate: true, HeartRate: 255}}, true},
		{"cumulative distance going backwards", []Sample{
			{HasDistance: true, Distance: 1000},
			{HasDistance: true, Distance: 900},
		}, true},
		{"distance holding steady while stopped", []Sample{
			{HasDistance: true, Distance: 1000},
			{HasDistance: true, Distance: 1000},
		}, false},
		// Absent fields carry meaningless values by design; bounding them
		// would fire on every sample a sensor did not report.
		{"absent fields are not bounded", []Sample{
			{HasSpeed: false, Speed: 99999, HasHeartRate: false, HeartRate: 255},
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scaleFault(c.samples)
			if hit := got != ""; hit != c.wantHit {
				t.Errorf("scaleFault = %q (fault=%v), want fault=%v", got, hit, c.wantHit)
			}
		})
	}
}

// TestIsFallbackDevFieldKey separates a resolved developer-field name from the
// "<developerDataIndex>.<fieldNum>" key used when a file registered a field
// but named it nowhere. Getting this backwards would make
// assertDevFieldsResolved report broken resolution as working, or the reverse.
func TestIsFallbackDevFieldKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"0.5", true},
		{"12.300", true},
		{"Power", false},
		{"Form Power", false},
		{"Leg Spring Stiffness", false},
		// A vendor is free to put a dot in a real name; only an all-numeric
		// pair is the fallback shape.
		{"Stryd.Power", false},
		{"", false},
		{".5", false},
		{"5.", false},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			if got := isFallbackDevFieldKey(c.key); got != c.want {
				t.Errorf("isFallbackDevFieldKey(%q) = %v, want %v", c.key, got, c.want)
			}
		})
	}
}
