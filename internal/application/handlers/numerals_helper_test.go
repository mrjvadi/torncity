package handlers

// faDigits used to translate an ASCII number into fa.yml's Perso-Arabic
// digits and thousands mark for a test's expectation. fa.yml now declares
// Western digits and a comma (format.digits, format.group_separator), the
// same as en, so a Persian screen writes numbers exactly as this string
// already does, and the function is the identity. Kept, rather than edited
// out of every call site, so a future locale change has one place to change
// it back.
func faDigits(s string) string { return s }
