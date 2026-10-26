// Package weather is the weather an activity was done in: hourly, from
// Open-Meteo's archive, at a few places along it, and at any sample by
// interpolating between the hours.
//
// It is a model's weather, not a measurement: ECMWF's IFS at about 9 km for
// the last few days, and the ERA5 reanalysis at about 25 km once it has
// caught up, five days on. A run through a city's streets or up a valley
// was in weather of its own the grid does not resolve, and the wind is the
// wind at 10 m in the open; what reaches a runner is less, and less again
// between buildings. It is still the best account there is of the weather
// where no sensor on the athlete measured it, and the temperature a watch
// measures on a wrist is no better: see fitactivity.TemperatureSource.
//
// Fetching sends the area and the hours of an activity to Open-Meteo, so a
// program does it only when its user asks. What it fetches is kept on disk:
// an answer about days the archive has settled is final, and one about the
// last few days, which the archive may yet replace with the reanalysis, is
// asked for again after a few hours.
//
// The data are Open-Meteo's under CC BY 4.0, which asks for a credit beside
// wherever they are shown: Credit.
package weather

import (
	"math"
	"time"
)

// Credit is what is shown beside the data, as their licence asks, and
// CreditURL where it links to.
const (
	Credit    = "Weather data by Open-Meteo.com"
	CreditURL = "https://open-meteo.com/"
)

// Conditions are the weather at one place and moment.
type Conditions struct {
	Time time.Time
	// Temperature and DewPoint are the air's at 2 m, in °C; Humidity is
	// the relative humidity there, in percent.
	Temperature, DewPoint, Humidity float64
	// WindSpeed and Gusts are at 10 m, in m/s. WindFrom is where the wind
	// blows from, in degrees clockwise from north, as a forecast gives it:
	// a westerly is 270.
	WindSpeed, Gusts, WindFrom float64
	// Pressure is the air's at the surface, in hPa.
	Pressure float64
	// Radiation is the sunlight reaching a level surface, in W/m²:
	// shortwave, direct and diffuse together.
	Radiation float64
}

// Place is the hourly weather at one point along an activity.
type Place struct {
	// Lat and Lon are where the weather was asked for; Elevation is the
	// height of the model's ground there, in metres.
	Lat, Lon, Elevation float64
	Hours               []Conditions
}

// Report is the weather an activity was done in.
type Report struct {
	Places []Place
	// Cached is true when every place was answered from the cache, with
	// nothing asked of Open-Meteo.
	Cached bool
}

// At is the weather at lat, lon -- the nearest place the report has -- at
// t, between the hours either side of it. A report with no place near
// enough, or no hour within one of t, has none. Without a position (hasPos
// false) the first place is taken: the activity's start.
func (r *Report) At(lat, lon float64, hasPos bool, t time.Time) (Conditions, bool) {
	if r == nil || len(r.Places) == 0 {
		return Conditions{}, false
	}
	p := &r.Places[0]
	if hasPos {
		best := math.Inf(1)
		for i := range r.Places {
			if d := greatCircle(lat, lon, r.Places[i].Lat, r.Places[i].Lon); d < best {
				best, p = d, &r.Places[i]
			}
		}
	}
	return p.at(t)
}

// at interpolates the place's hours at t. The wind's direction is taken
// from its vector, so a wind backing from 350° to 10° passes through north
// rather than south; its speed is interpolated as a speed, since averaging
// vectors would calm a wind that only turned.
func (p *Place) at(t time.Time) (Conditions, bool) {
	h := p.Hours
	if len(h) == 0 || t.Before(h[0].Time.Add(-time.Hour)) || t.After(h[len(h)-1].Time.Add(time.Hour)) {
		return Conditions{}, false
	}
	i := 0
	for i+1 < len(h) && !h[i+1].Time.After(t) {
		i++
	}
	if i+1 == len(h) || t.Before(h[i].Time) {
		c := h[i]
		c.Time = t
		return c, true
	}
	a, b := h[i], h[i+1]
	f := float64(t.Sub(a.Time)) / float64(b.Time.Sub(a.Time))
	mix := func(x, y float64) float64 { return x + (y-x)*f }
	ua, va := toward(a.WindFrom)
	ub, vb := toward(b.WindFrom)
	u, v := mix(ua, ub), mix(va, vb)
	from := a.WindFrom
	if u != 0 || v != 0 {
		from = math.Mod(math.Atan2(u, v)*180/math.Pi+360, 360)
	}
	return Conditions{
		Time:        t,
		Temperature: mix(a.Temperature, b.Temperature),
		DewPoint:    mix(a.DewPoint, b.DewPoint),
		Humidity:    mix(a.Humidity, b.Humidity),
		WindSpeed:   mix(a.WindSpeed, b.WindSpeed),
		Gusts:       mix(a.Gusts, b.Gusts),
		WindFrom:    from,
		Pressure:    mix(a.Pressure, b.Pressure),
		Radiation:   mix(a.Radiation, b.Radiation),
	}, true
}

// toward is the unit vector of a direction, east and north.
func toward(deg float64) (east, north float64) {
	r := deg * math.Pi / 180
	return math.Sin(r), math.Cos(r)
}

// Relative is the wind as someone moving on heading met it: head, the part
// of it blowing against them -- negative for a tailwind -- and cross, the
// part blowing across, positive from their right. Both in the units of
// c.WindSpeed.
func Relative(c Conditions, heading float64) (head, cross float64) {
	a := (c.WindFrom - heading) * math.Pi / 180
	return c.WindSpeed * math.Cos(a), c.WindSpeed * math.Sin(a)
}

// Compass is a direction as one of the sixteen points: 270 is "W", 200 is
// "SSW".
func Compass(deg float64) string {
	points := [...]string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}
	return points[int(math.Mod(deg+11.25+360, 360)/22.5)%16]
}

const earthRadius = 6371008.8

// greatCircle is the distance between two places on the sphere, in metres.
func greatCircle(lat0, lon0, lat1, lon1 float64) float64 {
	const rad = math.Pi / 180
	dLat, dLon := (lat1-lat0)*rad, (lon1-lon0)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat0*rad)*math.Cos(lat1*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadius * math.Asin(math.Min(1, math.Sqrt(a)))
}

// bearing is the initial direction from one place to another, in degrees
// clockwise from north.
func bearing(lat0, lon0, lat1, lon1 float64) float64 {
	const rad = math.Pi / 180
	y := math.Sin((lon1-lon0)*rad) * math.Cos(lat1*rad)
	x := math.Cos(lat0*rad)*math.Sin(lat1*rad) - math.Sin(lat0*rad)*math.Cos(lat1*rad)*math.Cos((lon1-lon0)*rad)
	return math.Mod(math.Atan2(y, x)/rad+360, 360)
}
