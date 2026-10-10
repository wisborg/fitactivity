package fitactivity

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// fr24Header is the first line of a Flightradar24 flight's CSV export,
// exactly: the one CSV this package reads. A CSV is a shape, not a format --
// any spreadsheet is one -- so it is recognised by this header alone, and a
// file with any other is refused rather than guessed at.
const fr24Header = "Timestamp,UTC,Callsign,Position,Altitude,Speed,Direction"

const (
	feet  = 0.3048        // metres
	knots = 1852.0 / 3600 // metres a second
)

// readFR24 reads a Flightradar24 flight exported as CSV: a row per report
// the aircraft's transponder made, with its time, position, altitude in
// feet, ground speed in knots and track in degrees.
//
// Altitude becomes Elevation, ground speed Speed and track Course, in
// metres, metres a second and degrees, and the callsign the Track's
// Flight. The same flight's KML export carries the altitude too, as
// a coordinate's third value, but its speed only inside the HTML of a
// placemark's description -- for a pop-up in Google Earth -- which is why
// this file is read and the description is not. The callsign names the
// flight -- THA950, the airline's ICAO code -- and the CSV has neither the
// flight number passengers know nor the airports; the same flight's KML
// does.
//
// Speed is over the ground, which is what the other formats' speed is too:
// an aircraft's airspeed is a different number, and this is not it.
//
// Every row is a flight's, so the Track's sport is flying.
func readFR24(data []byte) (*recording, error) {
	r := csv.NewReader(bytes.NewReader(withoutBOM(data)))
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("decoding Flightradar24 CSV: %w", err)
	}
	if strings.Join(header, ",") != fr24Header {
		return nil, errors.New("decoding Flightradar24 CSV: the header is not Flightradar24's")
	}
	rec := &recording{sport: sportName("flying")}
	for line := 2; ; line++ {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decoding Flightradar24 CSV: %w", err)
		}
		f, err := fr24Fix(row)
		if err != nil {
			return nil, fmt.Errorf("decoding Flightradar24 CSV line %d: %w", line, err)
		}
		rec.points = append(rec.points, f)
		if cs := strings.TrimSpace(row[2]); cs != "" && len(rec.flights) == 0 {
			rec.flights = []Flight{{Callsign: cs}}
		}
	}
	return rec, nil
}

// fr24Fix is one row of a Flightradar24 CSV. A field left empty is absent,
// not zero.
func fr24Fix(row []string) (fix, error) {
	var f fix
	sec, err := strconv.ParseInt(row[0], 10, 64)
	if err != nil {
		return f, fmt.Errorf("timestamp %q: %w", row[0], err)
	}
	f.Time, f.timed = time.Unix(sec, 0).UTC(), true
	if pos := strings.TrimSpace(row[3]); pos != "" {
		lat, lon, ok := strings.Cut(pos, ",")
		if !ok {
			return f, fmt.Errorf("position %q is not lat,lon", pos)
		}
		if f.Lat, err = strconv.ParseFloat(strings.TrimSpace(lat), 64); err != nil {
			return f, fmt.Errorf("latitude %q: %w", lat, err)
		}
		if f.Lon, err = strconv.ParseFloat(strings.TrimSpace(lon), 64); err != nil {
			return f, fmt.Errorf("longitude %q: %w", lon, err)
		}
		if f.Lat < -90 || f.Lat > 90 || f.Lon < -180 || f.Lon > 180 {
			return f, fmt.Errorf("position %q is off the globe", pos)
		}
		f.HasGPS = true
	}
	if alt := strings.TrimSpace(row[4]); alt != "" {
		ft, err := strconv.ParseFloat(alt, 64)
		if err != nil {
			return f, fmt.Errorf("altitude %q: %w", alt, err)
		}
		f.Elevation, f.HasElevation = ft*feet, true
	}
	if spd := strings.TrimSpace(row[5]); spd != "" {
		kt, err := strconv.ParseFloat(spd, 64)
		if err != nil {
			return f, fmt.Errorf("speed %q: %w", spd, err)
		}
		f.Speed, f.HasSpeed = kt*knots, true
	}
	if dir := strings.TrimSpace(row[6]); dir != "" {
		deg, err := strconv.ParseFloat(dir, 64)
		if err != nil || deg < 0 || deg > 360 {
			return f, fmt.Errorf("direction %q is not degrees from 0 to 360", dir)
		}
		f.Course, f.HasCourse = math.Mod(deg, 360), true
	}
	return f, nil
}
