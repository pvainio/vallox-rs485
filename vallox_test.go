package valloxrs485

import (
	"testing"
	"time"
)

func TestOutGoingAllowed(t *testing.T) {
	v := new(Vallox)
	assertBoolean(true, isOutgoingAllowed(v, 0), t)
	assertBoolean(false, isOutgoingAllowed(v, FanSpeed), t)
	assertBoolean(false, isOutgoingAllowed(v, TempIncomingInside), t)
	v.writeAllowed = true
	assertBoolean(true, isOutgoingAllowed(v, 0), t)
	assertBoolean(true, isOutgoingAllowed(v, FanSpeed), t)
	assertBoolean(false, isOutgoingAllowed(v, TempIncomingInside), t)
}

func TestValueToTemp(t *testing.T) {
	assertTemp(0, -74, t)
	assertTemp(255, 100, t)
	assertTemp(246, 97, t)
	assertTemp(247, 100, t)
}

func TestValueToSpeed(t *testing.T) {
	assertSpeed(1, 1, t)
	assertSpeed(3, 2, t)
	assertSpeed(7, 3, t)
	assertSpeed(15, 4, t)
	assertSpeed(31, 5, t)
	assertSpeed(63, 6, t)
	assertSpeed(127, 7, t)
	assertSpeed(255, 8, t)
}

func assertBoolean(expected bool, value bool, t *testing.T) {
	if expected != value {
		t.Errorf("exptected %v got %v", expected, value)
	}
}

func assertTemp(raw byte, value int16, t *testing.T) {
	if c, _ := valueToTemp(raw, nil); c != value {
		t.Errorf("temp %d was not converted to %d but to %d", raw, value, c)
	}
}

func assertSpeed(raw byte, value int16, t *testing.T) {
	if c, _ := valueToSpeed(raw, nil); c != value {
		t.Errorf("raw %d to speed was not converted to %d but to %d", raw, value, c)
	}

	if c := speedToValue(int8(value)); c != raw {
		t.Errorf("speed %d to raw was not converted to %d but to %d", value, raw, c)
	}
}

func TestValueToRh(t *testing.T) {
	assertRh(t, 0x33, 0)
	assertRh(t, 0xff, 100)
	assertRh(t, 0x99, 50)

	if _, ok := valueToRh(0x32, nil); ok {
		t.Errorf("vallox rh 0x32 expected !ok, but was ok")
	}
}

func assertRh(t *testing.T, valloxValue byte, rh int16) {
	if v, _ := valueToRh(valloxValue, nil); v != rh {
		t.Errorf("vallox rh %d expexted rh %d but was %d", valloxValue, rh, v)
	}
}

func TestValueToCo2(t *testing.T) {
	v := new(Vallox)
	e := event(&valloxPackage{Register: Co2HighestHighByte, Value: 1}, v)
	if e != nil {
		t.Errorf("expected no value, but got one")
	}
	e = event(&valloxPackage{Register: Co2HighestLowByte, Value: 0xf4}, v)
	if e.Value != 0x1f4 {
		t.Errorf("expected 0x1fe but got %x", e.Value)
	}
	if e.Register != Co2HighestLowByte {
		t.Errorf("expected register %d but got %d", Co2HighestLowByte, e.Register)
	}
}

func TestDelayedToCo2(t *testing.T) {
	v := new(Vallox)
	e := event(&valloxPackage{Register: Co2HighestHighByte, Value: 1}, v)
	if e != nil {
		t.Errorf("expected no value, but got one")
	}
	time.Sleep(600 * time.Millisecond)
	e = event(&valloxPackage{Register: Co2HighestLowByte, Value: 0xf4}, v)
	if e != nil {
		t.Errorf("expected no value, but got one")
	}
}
