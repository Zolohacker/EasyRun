package activity

// DistanceQuotaToleranceM matches the 100-metre distance selection granularity.
// A planned daily or weekly total may cross the configured boundary by at most
// one selection step before it is treated as exceeding the quota.
const DistanceQuotaToleranceM = 100.0

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

// ExceedsDistanceQuota reports whether adding a planned run would cross both
// the configured distance limit and the permitted 100-metre boundary window.
func ExceedsDistanceQuota(completedM, plannedM, limitM float64) bool {
	return limitM > 0 && completedM+plannedM > limitM+DistanceQuotaToleranceM
}
