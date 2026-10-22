package activity

// ResolveDailyRunLimit returns the effective per-day run-count ceiling.
// A non-positive serverMax means that the server did not set a run-count
// ceiling, represented by 0. When a ceiling exists, an omitted requested
// value defaults to the server maximum and an oversized value is clamped.
func ResolveDailyRunLimit(requested, serverMax int) int {
	if serverMax <= 0 {
		return 0
	}
	if requested <= 0 || requested > serverMax {
		return serverMax
	}
	return requested
}
