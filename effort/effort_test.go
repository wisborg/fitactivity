package effort

import (
	"math"
	"testing"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// On the flat in still air at sea level, the equivalent speed is the speed.
func TestEquivalent_FlatStillAirIsItself(t *testing.T) {
	for _, v := range []float64{2, 3, 4.5, 6} {
		if u := Equivalent(v, 0, 0, SeaLevelDensity); !near(u, v, 1e-6) {
			t.Errorf("%v m/s: %v", v, u)
		}
	}
}

// A headwind is an effort a still-air speed is faster for, and a tailwind
// slower; uphill likewise, and thinner air easier.
func TestEquivalent_Directions(t *testing.T) {
	v := 3.5
	if u := Equivalent(v, 0, 3, SeaLevelDensity); u <= v {
		t.Errorf("headwind: %v", u)
	}
	if u := Equivalent(v, 0, -3, SeaLevelDensity); u >= v {
		t.Errorf("tailwind: %v", u)
	}
	if u := Equivalent(v, 0.05, 0, SeaLevelDensity); u <= v {
		t.Errorf("uphill: %v", u)
	}
	if u := Equivalent(v, 0, 0, 1.0); u >= v {
		t.Errorf("thin air: %v", u)
	}
}

// Still air at marathon pace costs about Pugh's share: the air's part of
// the cost at 5.7 m/s is several per cent, and at an easy 3 m/s about two.
func TestCost_AirShare(t *testing.T) {
	for v, want := range map[float64]float64{3: 0.024, 5.7: 0.08} {
		c := Cost(v, 0, 0, SeaLevelDensity)
		if share := (c - HillCost(0)) / c; !near(share, want, 0.01) {
			t.Errorf("%v m/s: air is %.3f of the cost, want about %.3f", v, share, want)
		}
	}
}

// Without the headwind the same effort is faster, and StillAir undoes a
// tailwind the other way.
func TestStillAir(t *testing.T) {
	v := 3.0
	if u := StillAir(v, 0, 2, SeaLevelDensity); u <= v {
		t.Errorf("headwind removed: %v", u)
	}
	if u := StillAir(v, 0, -2, SeaLevelDensity); u >= v {
		t.Errorf("tailwind removed: %v", u)
	}
	if u := StillAir(v, 0.03, 0, SeaLevelDensity); !near(u, v, 1e-6) {
		t.Errorf("no wind: %v", u)
	}
}

func TestAirDensity(t *testing.T) {
	if d := AirDensity(1013.25, 15); !near(d, SeaLevelDensity, 0.001) {
		t.Errorf("standard atmosphere: %v", d)
	}
}

// Stull's own example: 20 °C at 50% is a wet bulb of 13.7 °C, so a shade
// WBGT of 0.7 · 13.7 + 0.3 · 20 = 15.6; air saturated is its own wet bulb.
func TestWBGT(t *testing.T) {
	if w := WetBulb(20, 50); !near(w, 13.7, 0.1) {
		t.Errorf("wet bulb at 20 °C, 50%%: %v", w)
	}
	if w := WBGT(20, 50); !near(w, 15.6, 0.1) {
		t.Errorf("WBGT at 20 °C, 50%%: %v", w)
	}
	if w := WBGT(24, 99); !near(w, 24, 0.3) {
		t.Errorf("WBGT at 24 °C, 99%%: %v", w)
	}
}

func TestHeatLoss(t *testing.T) {
	if HeatLoss(10) != 0 || !near(HeatLoss(20), 0.02, 1e-9) || !near(HeatLoss(2.5), 0.015, 1e-9) {
		t.Errorf("10: %v, 20: %v, 2.5: %v", HeatLoss(10), HeatLoss(20), HeatLoss(2.5))
	}
}

// At 1,300 m a runner has lost 6.3% of their oxygen uptake, and the same
// effort at sea level is faster; below 300 m nothing changes.
func TestAltitude(t *testing.T) {
	if l := AltitudeLoss(1300); !near(l, 0.063, 1e-9) || AltitudeLoss(200) != 0 {
		t.Errorf("1300 m: %v, 200 m: %v", l, AltitudeLoss(200))
	}
	if u := AtSeaLevel(3, 0, 0, 1.08, AltitudeLoss(1300)); u <= 3*1.05 || u >= 3*1.08 {
		t.Errorf("at sea level: %v", u)
	}
	if u := AtSeaLevel(3, 0, 0, SeaLevelDensity, 0); u != 3 {
		t.Errorf("no loss: %v", u)
	}
}
