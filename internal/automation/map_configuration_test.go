package automation

import (
	"context"
	"errors"
	"testing"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/store"
)

type mapConfigurationStore struct {
	store.Store
	settings platform.SystemSettings
	err      error
	jobs     []platform.AutomationJob
}

func (s mapConfigurationStore) GetSystemSettings(context.Context) (platform.SystemSettings, error) {
	return s.settings, s.err
}
func (s mapConfigurationStore) GetAccount(context.Context, string) (platform.Account, error) {
	return platform.Account{ID: "a"}, nil
}
func (s mapConfigurationStore) ListJobs(context.Context) ([]platform.AutomationJob, error) {
	return s.jobs, nil
}
func (s mapConfigurationStore) GetPlan(context.Context, string) (platform.RunningPlan, error) {
	return platform.RunningPlan{ID: "p", SourceAccountID: "a", RemoteCode: "p", TargetDistanceM: 1000, Rules: activity.RunRules{MinDistanceM: 1000, MaxDistanceM: 3000}}, nil
}
func (s mapConfigurationStore) GetArea(context.Context, string) (platform.RunningArea, error) {
	return platform.RunningArea{ID: "r", SourceAccountID: "a", RemoteCode: "r", Center: geo.Point{Lat: 35, Lng: 116}}, nil
}
func (s mapConfigurationStore) GetManualRoute(context.Context, string) (platform.ManualRoute, error) {
	p := geo.Point{Lat: 35, Lng: 116}
	return platform.ManualRoute{ID: "drawing", Name: "fixture", AccountID: "a", PlanID: "p", AreaID: "r", Points: []geo.Point{p, geo.OffsetMeters(p, 2000, 0)}}, nil
}
func TestMapConfigurationCapability(t *testing.T) {
	for _, tc := range []struct {
		name         string
		settings     platform.SystemSettings
		err          error
		want, failed bool
	}{
		{name: "uninitialized", err: store.ErrNotFound},
		{name: "empty"},
		{name: "empty members", settings: platform.SystemSettings{MapAPIKeys: []platform.MapAPIKey{{ID: "empty"}}}},
		{name: "one configured", settings: platform.SystemSettings{MapAPIKeys: []platform.MapAPIKey{{KeyCipher: []byte("cipher")}}}, want: true},
		{name: "later member", settings: platform.SystemSettings{MapAPIKeys: []platform.MapAPIKey{{}, {KeyCipher: []byte("cipher")}}}, want: true},
		{name: "legacy", settings: platform.SystemSettings{MapAPIKeyCipher: []byte("cipher")}, want: true},
		{name: "error fails closed", err: errors.New("database offline"), failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MapAPIConfigured(context.Background(), mapConfigurationStore{settings: tc.settings, err: tc.err})
			if got != tc.want || (err != nil) != tc.failed {
				t.Fatalf("configured=%v error=%v", got, err)
			}
		})
	}
}
func TestNewTaskRequiresMapKeyOnlyForAutomaticRoutes(t *testing.T) {
	for _, tc := range []struct {
		name, mode                             string
		configured, enabled, existing, wantErr bool
	}{
		{name: "new paused default needs key", wantErr: true},
		{name: "new enabled auto needs key", mode: "auto", enabled: true, wantErr: true},
		{name: "new custom without key", mode: "custom", enabled: true},
		{name: "new auto with key", mode: "auto", configured: true, enabled: true},
		{name: "existing auto pause permitted", mode: "auto", existing: true},
		{name: "existing auto enable requires key", mode: "auto", existing: true, enabled: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := mapConfigurationStore{}
			job := platform.AutomationJob{ID: "job", AccountID: "a", PlanID: "p", AreaID: "r", TargetDistanceM: 1000, RouteMode: tc.mode, ManualRouteID: "drawing", Enabled: tc.enabled}
			if tc.configured {
				db.settings.MapAPIKeys = []platform.MapAPIKey{{KeyCipher: []byte("fixture")}}
			}
			if tc.existing {
				db.jobs = []platform.AutomationJob{job}
			}
			if err := NormalizeJob(context.Background(), db, &job); (err != nil) != tc.wantErr {
				t.Fatalf("NormalizeJob error=%v expected error=%v", err, tc.wantErr)
			}
		})
	}
}
