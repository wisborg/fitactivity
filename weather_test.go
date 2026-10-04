package fitactivity

import "testing"

// The footpod's temperature is preferred, the watch's taken without it, and
// a forced source is that source or nothing; humidity is the footpod's or
// nothing.
func TestResolvedTemperatureAndHumidity(t *testing.T) {
	both := Sample{HasTemperature: true, Temperature: 24, DevFields: map[string]float64{StrydTemperatureField: 17.5, StrydHumidityField: 55}}
	watch := Sample{HasTemperature: true, Temperature: 24}
	none := Sample{}
	for _, c := range []struct {
		s    Sample
		src  TemperatureSource
		want float64
		ok   bool
	}{
		{both, TemperatureAuto, 17.5, true},
		{both, TemperatureStryd, 17.5, true},
		{both, TemperatureNative, 24, true},
		{watch, TemperatureAuto, 24, true},
		{watch, TemperatureStryd, 0, false},
		{none, TemperatureAuto, 0, false},
		{none, TemperatureNative, 0, false},
	} {
		if v, ok := c.s.ResolvedTemperature(c.src); v != c.want || ok != c.ok {
			t.Errorf("%+v %v: %v %v, want %v %v", c.s, c.src, v, ok, c.want, c.ok)
		}
	}
	if h, ok := both.Humidity(); h != 55 || !ok {
		t.Errorf("humidity %v %v", h, ok)
	}
	if _, ok := watch.Humidity(); ok {
		t.Error("a watch's sample has a humidity")
	}
	for src, want := range map[TemperatureSource]string{TemperatureAuto: "auto", TemperatureStryd: "stryd", TemperatureNative: "native", 7: "unknown"} {
		if src.String() != want {
			t.Errorf("%d is %q, want %q", src, src.String(), want)
		}
	}
}
