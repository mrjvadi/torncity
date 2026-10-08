package handlers

import (
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Paying a settlement in its own money (roadmap 2.19 phase 2b, docs/adr/0033 section 6.9 and 6.10).
//
// An obligation to a settlement that has chartered its money settles in that money when the payer holds
// enough units; a payer who holds SUP instead is never blocked: the confirm offers to convert at the
// village desk inside the same step (the Offer on the response, then the same command again with
// convert and max_sup), and a payer who does nothing settles in SUP exactly as before.

// LocalSettle is the part of a confirm's request that asks to convert at the desk and pay: Convert is "1"
// and MaxSUP the SUP the payer was shown for the conversion (the price protection: the confirm is
// refused when the desk now asks more).
type LocalSettle struct {
	Convert string `json:"convert,omitempty"`
	MaxSUP  string `json:"max_sup,omitempty"`
}

func (l LocalSettle) wantsConvert() bool { return strings.TrimSpace(l.Convert) == "1" }

func (l LocalSettle) maxSUP() int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(l.MaxSUP), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// attachOffer sets the offer on a confirm's response and, when the payer is short and the desk can fill
// the gap, the action that converts and pays: the response's own confirm action with convert and max_sup
// added after its `convertAt` positional arguments (the empty ones in between keep the order).
func attachOffer(resp *presentation.Response, o *application.LocalOffer, convertAt int) *presentation.Response {
	return attachOfferTo(resp, o, convertAt, func(a presentation.Action) bool { return a.Role == presentation.RoleConfirm })
}

// attachOfferCmd is attachOffer for a screen whose pay buttons are the command `command` (the first
// one carries the arguments the converting action repeats).
func attachOfferCmd(resp *presentation.Response, o *application.LocalOffer, command string, convertAt int) *presentation.Response {
	return attachOfferTo(resp, o, convertAt, func(a presentation.Action) bool { return a.Command == command })
}

func attachOfferTo(resp *presentation.Response, o *application.LocalOffer, convertAt int, match func(presentation.Action) bool) *presentation.Response {
	if resp == nil || o == nil {
		return resp
	}
	v := &presentation.LocalOffer{
		Settlement: o.SettlementID, Code: o.Code, Name: o.Name, SUP: o.SUP, Units: o.Units, Holds: o.Holds,
		Local: o.Local, CanConvert: o.CanConvert, ConvertSUP: o.ConvertSUP, ConvertFee: o.ConvertFee,
		ConvertUnits: o.ConvertUnits, FeeBPS: o.FeeBPS,
	}
	if o.CanConvert {
		for _, a := range resp.Actions {
			if !match(a) {
				continue
			}
			args := append([]string(nil), a.Args...)
			for len(args) < convertAt {
				args = append(args, "")
			}
			args = append(args[:convertAt], "1", strconv.FormatInt(o.ConvertSUP, 10))
			c := presentation.Action{ID: "convert", Command: a.Command, Args: args, Role: presentation.RolePrimary}
			c = c.With("convert", "1").With("max_sup", strconv.FormatInt(o.ConvertSUP, 10))
			for k, val := range a.Params {
				if _, set := c.Params[k]; !set {
					c = c.With(k, val)
				}
			}
			v.Convert = &c
			break
		}
	}
	resp.Offer = v
	return resp
}

// deskRefusal maps a desk refusal met while converting inside a confirm to a village refusal; false when
// the error is not one of the desk's.
func deskRefusal(err error) (error, bool) {
	switch {
	case stderrors.Is(err, application.ErrDeskEmpty):
		return refuseVillage(village.VillageDeskEmpty), true
	case stderrors.Is(err, application.ErrDeskNoSUP):
		return refuseVillage(village.VillageDeskNoSUP), true
	case stderrors.Is(err, application.ErrDeskFunds):
		return refuseVillage(village.VillageDeskFunds), true
	case stderrors.Is(err, application.ErrDeskMoved):
		return refuseVillage(village.VillageDeskMoved), true
	case stderrors.Is(err, application.ErrDeskNone):
		return refuseVillage(village.VillageNotAvailable), true
	}
	return nil, false
}
