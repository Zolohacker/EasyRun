package activity

import "testing"

func TestResolveDailyRunLimit(t *testing.T) {
	tests := []struct {
		name      string
		requested int
		serverMax int
		want      int
	}{
		{name: "server has no count limit", requested: 1, serverMax: 0, want: 0},
		{name: "server has no count limit and request omitted", requested: 0, serverMax: 0, want: 0},
		{name: "request omitted uses server maximum", requested: 0, serverMax: 3, want: 3},
		{name: "requested value is retained", requested: 2, serverMax: 3, want: 2},
		{name: "requested value is clamped", requested: 4, serverMax: 3, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveDailyRunLimit(tt.requested, tt.serverMax); got != tt.want {
				t.Fatalf("ResolveDailyRunLimit(%d, %d) = %d, want %d", tt.requested, tt.serverMax, got, tt.want)
			}
		})
	}
}

func TestExceedsDistanceQuota(t *testing.T) {
	tests := []struct {
		name       string
		completedM float64
		plannedM   float64
		limitM     float64
		want       bool
	}{
		{name: "twenty metre overage is allowed", completedM: 2020, plannedM: 3000, limitM: 5000, want: false},
		{name: "exactly one hundred metre overage is allowed", completedM: 2100, plannedM: 3000, limitM: 5000, want: false},
		{name: "more than one hundred metre overage is rejected", completedM: 2101, plannedM: 3000, limitM: 5000, want: true},
		{name: "zero limit is unrestricted", completedM: 9000, plannedM: 3000, limitM: 0, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExceedsDistanceQuota(tt.completedM, tt.plannedM, tt.limitM); got != tt.want {
				t.Fatalf("ExceedsDistanceQuota(%v, %v, %v) = %v, want %v", tt.completedM, tt.plannedM, tt.limitM, got, tt.want)
			}
		})
	}
}
