package life

import (
	stderrors "errors"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// ScreenError is a command the game refused or could not run: its code says
// why and its args hold the numbers, and each edge words it. The name is part
// of the client contract.
const ScreenError = "error"

// ErrorView is a refused or failed command as data.
type ErrorView struct {
	// Code names why, in the stable dotted form the edges word it by:
	// "error.at_work", "bank.error.below_minimum", "travel.same_city". A
	// failure nobody anticipated is "error.internal" (or, by its class,
	// "error.not_found", "error.conflict"...).
	Code string
	// Args are what the sentence needs, as data: money in minor units,
	// durations in seconds, offices and other content by code. Nothing in
	// them is text.
	Args map[string]any
}

var screenError = presentation.Define[ErrorView](ScreenError, "life")

// Error is the screen of a refusal or a failure, with the one next step it
// has (the journey for «already travelling», the bank for a short purse) and
// the way back.
func Error(c presentation.Ctx, v ErrorView) *presentation.Response {
	var a []presentation.Action
	if n, ok := errorNext[v.Code]; ok {
		a = append(a, act(n.addr).Named(n.id))
	}
	a = append(a, back(AddrHome))
	return screenError.Response(c.Lang, v, a...)
}

// errorNext is the one screen that resolves a refusal. A refusal missing from
// here gets the way back alone.
var errorNext = map[string]struct{ id, addr string }{
	"error.already_travelling":      {"profile.journey", AddrTravelStatus},
	"error.at_work":                 {"job.mine", AddrJobStatus},
	"travel.none":                   {"profile.map", AddrMap},
	"travel.same_city":              {"profile.map", AddrMap},
	"travel.no_route":               {"profile.map", AddrMap},
	"travel.mode_unavailable":       {"profile.map", AddrMap},
	"error.city_not_found":          {"profile.map", AddrMap},
	"error.skill_not_found":         {"skills", AddrSkills},
	"error.not_friends":             {"social", AddrFriendList},
	"error.already_friends":         {"social", AddrFriendList},
	"error.unsupported_language":    {"settings", AddrSettings},
	"bank.error.not_in_city":        {"bank", AddrBank},
	"bank.error.not_enough_cash":    {"bank", AddrBank},
	"bank.error.not_enough_in_bank": {"bank", AddrBank},
	"bank.error.insufficient":       {"bank", AddrBank},
	"bank.error.payee_not_found":    {"find_player", AddrSearch},
	"bank.error.invalid_amount":     {"bank", AddrBank},
	"bank.error.below_minimum":      {"bank", AddrBank},
	"bank.error.above_maximum":      {"bank", AddrBank},
	"crime.error.in_jail":           {"profile.jail", AddrCrimeJail},
	"crime.error.in_jail_later":     {"profile.jail", AddrCrimeJail},
	"crime.error.in_progress":       {"crime.hub", AddrCrimeHub},
	"health.error.hospitalised":     {"profile.hospital", AddrHospital},
	"health.error.hospitalised_later": {"profile.hospital", AddrHospital},
}

// sentinelCodes maps the application's sentinels, matched by identity, to
// their codes. Identity and not class: errors.Is matches by code, and several
// of these share one (ErrCityNotFound, ErrNoActiveTravel, ErrSkillNotFound and
// ErrNotFriends are all NOT_FOUND), so the class cannot tell them apart.
var sentinelCodes = []struct {
	target error
	code   string
}{
	{application.ErrCityNotFound, "error.city_not_found"},
	{application.ErrNoActiveTravel, "travel.none"},
	{application.ErrAlreadyTravelling, "error.already_travelling"},
	{application.ErrShiftInProgress, "error.at_work"},
	{application.ErrSkillNotFound, "error.skill_not_found"},
	{application.ErrNotFriends, "error.not_friends"},
	{application.ErrAlreadyFriends, "error.already_friends"},
	{application.ErrPlayerNotFound, "error.player_not_found"},
	{application.ErrUnsupportedLanguage, "error.unsupported_language"},
	{application.ErrPaymentNotAccepted, "payment.not_accepted"},
}

// governanceCodes are the governance refusals that need no numbers.
var governanceCodes = []struct {
	target error
	code   string
}{
	{application.ErrUnknownLever, "gov.refusal.unknown_lever"},
	{application.ErrJurisdictionNotFound, "gov.refusal.unknown_place"},
	{application.ErrWrongJurisdiction, "gov.refusal.wrong_place"},
	{application.ErrCityTierOnly, "gov.refusal.city_only"},
	{application.ErrLeverKindUnsupported, "gov.refusal.unsupported"},
	{application.ErrInvalidAllocation, "gov.refusal.invalid_allocation"},
	{application.ErrOfficeNotFound, "gov.refusal.office_not_found"},
	{application.ErrOfficeOccupied, "gov.refusal.office_occupied"},
	{application.ErrOfficeVacant, "gov.refusal.office_vacant"},
	{application.ErrAlreadyHoldsSeat, "gov.refusal.already_holds"},
	{application.ErrIncompatibleOffices, "gov.refusal.incompatible"},
}

// ErrorOf classifies a failure into the code and the data the screen of it
// carries. A nil error is an internal failure.
func ErrorOf(err error) ErrorView {
	if err == nil {
		return ErrorView{Code: "error.internal"}
	}
	if v, ok := bankError(err); ok {
		return v
	}
	switch {
	case identical(err, application.ErrInJail):
		return secondsError("crime.error.in_jail", "crime.error.in_jail_later", err)
	case identical(err, application.ErrCrimeInProgress):
		return ErrorView{Code: "crime.error.in_progress"}
	case identical(err, application.ErrHospitalised):
		return secondsError("health.error.hospitalised", "health.error.hospitalised_later", err)
	}
	if v, ok := governanceError(err); ok {
		return v
	}
	for _, s := range sentinelCodes {
		if identical(err, s.target) {
			return ErrorView{Code: s.code}
		}
	}
	switch {
	case stderrors.Is(err, player.ErrNotEnoughEnergy):
		needed, current := detailInt(err, "needed"), detailInt(err, "current")
		if needed <= current {
			// Without the two numbers there is no honest wait to quote.
			return ErrorView{Code: "error.not_enough_energy_later"}
		}
		return ErrorView{Code: "error.not_enough_energy", Args: map[string]any{"needed": needed, "current": current}}
	case stderrors.Is(err, travel.ErrSameCity):
		return ErrorView{Code: "travel.same_city"}
	case stderrors.Is(err, world.ErrNoRoute), stderrors.Is(err, world.ErrUnknownCity):
		return ErrorView{Code: "travel.no_route"}
	case stderrors.Is(err, travel.ErrModeUnavailable):
		return ErrorView{Code: "travel.mode_unavailable"}
	}
	switch errors.CodeOf(err) {
	case errors.CodeNotFound:
		return ErrorView{Code: "error.not_found"}
	case errors.CodeInvalidInput:
		return ErrorView{Code: "error.invalid_input"}
	case errors.CodeConflict:
		return ErrorView{Code: "error.conflict"}
	case errors.CodeRateLimited:
		return ErrorView{Code: "error.rate_limited"}
	case errors.CodeCooldown:
		return ErrorView{Code: "error.cooldown", Args: map[string]any{"seconds": detailInt(err, "seconds")}}
	case errors.CodeUnauthorized:
		return ErrorView{Code: "error.unauthorized"}
	}
	return ErrorView{Code: "error.internal"}
}

// secondsError is a refusal with a time left: the plain code, or the "later"
// one when the error carries no honest number.
func secondsError(code, later string, err error) ErrorView {
	secs := detailInt(err, "remaining_seconds")
	if secs <= 0 {
		return ErrorView{Code: later}
	}
	return ErrorView{Code: code, Args: map[string]any{"remaining_seconds": secs}}
}

func bankError(err error) (ErrorView, bool) {
	switch {
	case stderrors.Is(err, application.ErrBankNotInCity):
		return ErrorView{Code: "bank.error.not_in_city"}, true
	case stderrors.Is(err, application.ErrNotTogether):
		return ErrorView{Code: "bank.error.not_together"}, true
	case stderrors.Is(err, application.ErrSelfPayment):
		return ErrorView{Code: "bank.error.self_payment"}, true
	case stderrors.Is(err, application.ErrPayeeNotFound):
		return ErrorView{Code: "bank.error.payee_not_found"}, true
	case stderrors.Is(err, application.ErrInvalidMoneyAmount):
		return ErrorView{Code: "bank.error.invalid_amount"}, true
	case stderrors.Is(err, application.ErrAmountBelowMinimum):
		return ErrorView{Code: "bank.error.below_minimum", Args: map[string]any{"min": detailInt(err, "min")}}, true
	case stderrors.Is(err, application.ErrAmountAboveMaximum):
		return ErrorView{Code: "bank.error.above_maximum", Args: map[string]any{"max": detailInt(err, "max")}}, true
	case stderrors.Is(err, application.ErrNotEnoughCash):
		return ErrorView{Code: "bank.error.not_enough_cash", Args: map[string]any{"available": detailInt(err, "available")}}, true
	case stderrors.Is(err, application.ErrNotEnoughInBank):
		return ErrorView{Code: "bank.error.not_enough_in_bank", Args: map[string]any{"available": detailInt(err, "available"), "needed": detailInt(err, "needed")}}, true
	case stderrors.Is(err, application.ErrInsufficientFunds):
		return ErrorView{Code: "bank.error.insufficient"}, true
	case stderrors.Is(err, application.ErrBankPolicyUnavailable):
		return ErrorView{Code: "bank.error.unavailable"}, true
	}
	return ErrorView{}, false
}

func governanceError(err error) (ErrorView, bool) {
	switch {
	case stderrors.Is(err, application.ErrNotOfficeHolder):
		return ErrorView{Code: "gov.refusal.not_holder", Args: map[string]any{"office": detailString(err, "office")}}, true
	case stderrors.Is(err, application.ErrPolicyRequiresConfirmation):
		return ErrorView{Code: "gov.refusal.requires_confirmation", Args: map[string]any{"office": detailString(err, "body")}}, true
	case stderrors.Is(err, application.ErrPolicyRequiresVote):
		return ErrorView{Code: "gov.refusal.requires_vote", Args: map[string]any{"office": detailString(err, "body")}}, true
	case stderrors.Is(err, application.ErrPolicyOutOfBounds):
		return ErrorView{Code: "gov.refusal.out_of_range_plain"}, true
	case stderrors.Is(err, application.ErrPolicyCooldown):
		return ErrorView{Code: "gov.refusal.cooldown_later"}, true
	}
	for _, s := range governanceCodes {
		if stderrors.Is(err, s.target) {
			return ErrorView{Code: s.code}, true
		}
	}
	return ErrorView{}, false
}

// identical reports whether target appears anywhere in err's chain as that
// exact value, ignoring the Is method entirely.
//
// A named sentinel (errors.Sentinel) is matched by its id as well, so a copy
// carrying details (ErrInJail.WithDetail("remaining_seconds", ...), as every
// refusal with a number is raised) is still the sentinel it was copied from.
// An unnamed one is matched by identity alone.
func identical(err, target error) bool {
	named, _ := target.(*errors.Error)
	for e := err; e != nil; e = stderrors.Unwrap(e) {
		if e == target {
			return true
		}
		if x, ok := e.(*errors.Error); ok && named != nil && named.ID() != "" && x.ID() == named.ID() {
			return true
		}
	}
	return false
}

// detailInt reads one structured detail off a classified error, or zero.
// Details are metadata, so only numbers a message needs are read back out.
func detailInt(err error, key string) int64 {
	var e *errors.Error
	if !stderrors.As(err, &e) {
		return 0
	}
	switch v := e.Details[key].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

// detailString reads one string detail off a classified error.
func detailString(err error, key string) string {
	var e *errors.Error
	if !stderrors.As(err, &e) {
		return ""
	}
	s, _ := e.Details[key].(string)
	return s
}
