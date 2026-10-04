package fitactivity

// StrydTemperatureField and StrydHumidityField are the developer-field keys a
// Stryd footpod registers the air temperature (°C) and relative humidity (%)
// it measures under, resolved from the FIT file's own FieldDescription
// messages as StrydPowerField is.
//
// The footpod's temperature is the air's, near enough: it is on a shoe, out
// in the wind. The watch's own, the standard Sample.Temperature, is taken on
// a wrist and reads warm -- 22 to 25 °C on a run where the footpod read 15 to
// 22 -- so the two are different readings of different things, as the two
// powers are, and which is shown is a choice: see TemperatureSource.
const (
	StrydTemperatureField = "Stryd Temperature"
	StrydHumidityField    = "Stryd Humidity"
)

// TemperatureSource selects which of a Sample's two possible temperature
// readings the caller wants: the footpod's developer field (Stryd), the
// standard FIT Record.temperature field (native), or automatic preference.
// Its values and their spelling are PowerSource's, so a tool that offers
// --power-source can offer --temperature-source in the same words.
type TemperatureSource int

const (
	// TemperatureAuto prefers the Stryd developer field when present -- the
	// air's temperature rather than a wrist's -- and falls back to the
	// native field otherwise.
	TemperatureAuto TemperatureSource = iota
	// TemperatureStryd forces the Stryd developer field; no native fallback.
	TemperatureStryd
	// TemperatureNative forces the standard FIT Record.temperature field.
	TemperatureNative
)

// String renders s the way a CLI spells it: auto, stryd, native.
func (s TemperatureSource) String() string {
	switch s {
	case TemperatureAuto:
		return "auto"
	case TemperatureStryd:
		return "stryd"
	case TemperatureNative:
		return "native"
	default:
		return "unknown"
	}
}

// ResolvedTemperature returns the temperature, in °C, for the given source
// preference, and whether one is present. As with ResolvedPower, a forced
// source is strict -- absent rather than the other sensor's number -- and
// TemperatureAuto prefers Stryd and falls back to native.
func (s Sample) ResolvedTemperature(src TemperatureSource) (float64, bool) {
	stryd, hasStryd := s.DevFields[StrydTemperatureField]
	switch src {
	case TemperatureNative:
		return float64(s.Temperature), s.HasTemperature
	case TemperatureStryd:
		return stryd, hasStryd
	default:
		if hasStryd {
			return stryd, true
		}
		return float64(s.Temperature), s.HasTemperature
	}
}

// Humidity returns the relative humidity, in per cent, and whether the
// sample carries one. A Stryd footpod is the only source: FIT's record
// message has no humidity field of its own.
func (s Sample) Humidity() (float64, bool) {
	h, ok := s.DevFields[StrydHumidityField]
	return h, ok
}
