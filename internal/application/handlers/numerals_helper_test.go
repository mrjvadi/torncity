package handlers

import "strings"

// persianDigits maps ASCII digits to the digits fa.yml's format.digits
// declares, so a test can say which number a Persian screen shows.
var persianDigits = strings.NewReplacer(
	"0", "۰", "1", "۱", "2", "۲", "3", "۳", "4", "۴",
	"5", "۵", "6", "۶", "7", "۷", "8", "۸", "9", "۹",
	",", "٬",
)

// faDigits writes an ASCII number as a Persian screen writes it.
func faDigits(s string) string { return persianDigits.Replace(s) }
