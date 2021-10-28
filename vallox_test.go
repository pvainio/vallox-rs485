package valloxrs485

import (
	"testing"
)

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

func assertTemp(raw byte, value int8, t *testing.T) {
	if c := valueToTemp(raw); c != value {
		t.Errorf("temp %d was not converted to %d but to %d", raw, value, c)
	}
}

func assertSpeed(raw byte, value int8, t *testing.T) {
	if c := valueToSpeed(raw); c != value {
		t.Errorf("raw %d to speed was not converted to %d but to %d", raw, value, c)
	}

	if c := speedToValue(value); c != raw {
		t.Errorf("speed %d to raw was not converted to %d but to %d", value, raw, c)
	}
}
