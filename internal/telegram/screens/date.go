package screens

import (
	"strconv"
	"time"
)

// Dates as a player reads them: the day, month and year of an instant in
// the context's zone, in the calendar of the language (format.calendar:
// "jalali" — the Solar Hijri calendar Iran uses — or "gregorian"), with the
// month's name from the catalogue (format.month.<n>) and the language's
// digits. The shape is format.date.

// FormatDate renders the date of t, or nothing for a zero t.
func FormatDate(c Context, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	local := t.In(c.zone())
	y, m, d := local.Year(), int(local.Month()), local.Day()
	if c.T("format.calendar", nil) == "jalali" {
		y, m, d = jalali(y, m, d)
	}
	n := c.numerals()
	return c.T("format.date", map[string]any{
		"day":   n.localise(strconv.Itoa(d)),
		"month": c.T("format.month.m"+strconv.Itoa(m), nil),
		"year":  n.localise(strconv.Itoa(y)),
	})
}

// jalali converts a Gregorian date to the Solar Hijri calendar (the
// arithmetic of the Iranian calendar's 33-year cycles, exact for the years
// this game will see).
func jalali(gy, gm, gd int) (int, int, int) {
	daysBefore := [...]int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	gy2 := gy
	if gm > 2 {
		gy2 = gy + 1
	}
	days := 355666 + 365*gy + (gy2+3)/4 - (gy2+99)/100 + (gy2+399)/400 + gd + daysBefore[gm-1]
	jy := -1595 + 33*(days/12053)
	days %= 12053
	jy += 4 * (days / 1461)
	days %= 1461
	if days > 365 {
		jy += (days - 1) / 365
		days = (days - 1) % 365
	}
	if days < 186 {
		return jy, 1 + days/31, 1 + days%31
	}
	return jy, 7 + (days-186)/30, 1 + (days-186)%30
}
