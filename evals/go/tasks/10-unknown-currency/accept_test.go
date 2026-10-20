package cache

import (
	"errors"
	"testing"
)

func TestAccept10UnknownCurrenciesAreAnError(t *testing.T) {
	c := New()
	_, err := c.Rate("XYZ")
	var unknown *UnknownCurrencyError
	if !errors.As(err, &unknown) || unknown.Code != "XYZ" {
		t.Errorf("Rate(XYZ) err = %v, want UnknownCurrencyError{XYZ}", err)
	}
	if r, err := c.Rate("EUR"); err != nil || r != 10850 {
		t.Errorf("Rate(EUR) = %d, %v; want 10850", r, err)
	}
	if _, ok := Quote("XYZ"); ok {
		t.Error("Quote(XYZ) is ok")
	}
}
