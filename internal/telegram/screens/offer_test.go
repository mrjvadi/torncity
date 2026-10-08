package screens

import (
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// A confirm tells the payer how the settlement's own money is involved; nothing is written when it is not.
func TestAnOfferIsWordedForEachWayItCanGo(t *testing.T) {
	c := ctx(t, "en", 0)
	if OfferText(c, nil) != "" {
		t.Error("no offer, no words")
	}
	local := &presentation.LocalOffer{Name: "Marco", SUP: 40, Units: 400, Holds: 900, Local: true}
	if got := OfferText(c, local); !strings.Contains(got, "400 Marco") {
		t.Errorf("holding enough reads as paid in the village money: %q", got)
	}
	conv := presentation.Action{ID: "convert", Command: "settlement.donate", Args: []string{"100", "confirm", "", "1", "1010"}}
	short := &presentation.LocalOffer{Name: "Marco", SUP: 100, Units: 1000, Holds: 0, CanConvert: true, ConvertSUP: 101, ConvertFee: 1, ConvertUnits: 1000, Convert: &conv}
	got := OfferText(c, short)
	if !strings.Contains(got, "101") || !strings.Contains(got, "1,000 Marco") && !strings.Contains(got, "1000 Marco") {
		t.Errorf("a payer short of units sees the desk's price and the gap: %q", got)
	}
	btn, ok := OfferButton(c, short)
	if !ok || btn.CallbackData != "settlement:donate:100:confirm::1:1010" {
		t.Errorf("the converting button repeats the confirm with convert and the shown price: %+v %v", btn, ok)
	}
	if _, ok := OfferButton(c, local); ok {
		t.Error("a payer who holds enough has nothing to convert")
	}
	// a payer who cannot convert and holds too little settles in SUP: the confirm says nothing about it
	if got := OfferText(c, &presentation.LocalOffer{Name: "Marco", Units: 400, Holds: 10}); got != "" {
		t.Errorf("nothing to say when the payment goes in SUP: %q", got)
	}
}
