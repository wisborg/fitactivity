package weather

// Sky is the kind of weather a WMO code is, as an icon would show it, in
// order of how much it matters to someone out in it: rain over cloud,
// thunder over rain.
type Sky int

const (
	Clear Sky = iota
	PartlyCloudy
	Overcast
	Fog
	Drizzle
	Rain
	Snow
	Thunder
)

// Kind is the kind of weather code is: 0 and 1 clear, 2 partly cloudy, 3
// overcast, 45 and 48 fog, 51-57 drizzle, 61-67 and 80-82 rain, 71-77 and
// 85-86 snow, 95-99 a thunderstorm.
func Kind(code int) Sky {
	switch {
	case code <= 1:
		return Clear
	case code == 2:
		return PartlyCloudy
	case code < 45:
		return Overcast
	case code < 51:
		return Fog
	case code < 61:
		return Drizzle
	case code < 71, code >= 80 && code < 85:
		return Rain
	case code < 95:
		return Snow
	}
	return Thunder
}

// descriptions are Open-Meteo's words for the WMO codes it gives.
var descriptions = map[int]string{
	0: "clear sky", 1: "mainly clear", 2: "partly cloudy", 3: "overcast",
	45: "fog", 48: "rime fog",
	51: "light drizzle", 53: "drizzle", 55: "dense drizzle", 56: "freezing drizzle", 57: "dense freezing drizzle",
	61: "light rain", 63: "rain", 65: "heavy rain", 66: "freezing rain", 67: "heavy freezing rain",
	71: "light snow", 73: "snow", 75: "heavy snow", 77: "snow grains",
	80: "light showers", 81: "showers", 82: "violent showers", 85: "snow showers", 86: "heavy snow showers",
	95: "thunderstorm", 96: "thunderstorm with hail", 99: "thunderstorm with heavy hail",
}

// Describe is a WMO weather code in words: 61 is "light rain". A code
// Open-Meteo does not give is described by its kind.
func Describe(code int) string {
	if d, ok := descriptions[code]; ok {
		return d
	}
	return [...]string{"clear sky", "partly cloudy", "overcast", "fog", "drizzle", "rain", "snow", "thunderstorm"}[Kind(code)]
}

// significant is the share of an activity's minutes a weather code must
// hold for to be what the activity is said to have been done in: a
// passing shower over a minute of a marathon is not its weather, one over
// half an hour is.
const significant = 0.1

// mostSignificant is the highest of the codes met over at least
// significant of the minutes -- the WMO's codes rise with how much the
// weather matters, cloud below drizzle below rain below thunder -- from
// the minutes each code was met.
func mostSignificant(minutes map[int]float64) int {
	var total float64
	for _, n := range minutes {
		total += n
	}
	best, most := -1, 0.0
	for code, n := range minutes {
		if n >= significant*total && code > best {
			best = code
		}
		if n > most {
			most = n
		}
	}
	if best < 0 {
		for code, n := range minutes {
			if n == most && code > best {
				best = code
			}
		}
	}
	return best
}
