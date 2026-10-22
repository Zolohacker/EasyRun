package activity

import "testing"

func TestSampleTargetsUsesMiddleSeventyAndKeepsKinematicsConsistent(t *testing.T) {
	rules := RunRules{
		MinDistanceM: 2000, MaxDistanceM: 5000,
		FastestPaceSecPerKM: 240, SlowestPaceSecPerKM: 600,
		MinCadenceSPM: 140, MaxCadenceSPM: 190,
		MaxSegmentStrideM: 1.35,
	}
	selection, err := SampleTargets(2000, Profile{}, rules, 42)
	if err != nil {
		t.Fatal(err)
	}
	if selection.TargetDistanceM < 2450 || selection.TargetDistanceM > 4550 {
		t.Fatalf("distance %.1f outside middle 70%%", selection.TargetDistanceM)
	}
	if selection.Profile.TargetPaceSecPerKM < 294 || selection.Profile.TargetPaceSecPerKM > 546 {
		t.Fatalf("pace %.1f outside middle 70%%", selection.Profile.TargetPaceSecPerKM)
	}
	if selection.Profile.CadenceSPM < 147.5 || selection.Profile.CadenceSPM > 182.5 {
		t.Fatalf("cadence %.1f outside middle 70%%", selection.Profile.CadenceSPM)
	}
	derived := 60000 / (selection.Profile.TargetPaceSecPerKM * selection.Profile.CadenceSPM)
	if delta := derived - selection.Profile.TargetStrideM; delta < -0.002 || delta > 0.002 {
		t.Fatalf("stride %.3f is inconsistent with derived %.3f", selection.Profile.TargetStrideM, derived)
	}
	if selection.Profile.TargetStrideM >= rules.MaxSegmentStrideM {
		t.Fatalf("stride %.3f reaches server threshold %.3f", selection.Profile.TargetStrideM, rules.MaxSegmentStrideM)
	}

	again, err := SampleTargets(2000, Profile{}, rules, 42)
	if err != nil {
		t.Fatal(err)
	}
	if again.TargetDistanceM != selection.TargetDistanceM || again.Profile != selection.Profile {
		t.Fatal("same seed must produce the same selection")
	}
}

func TestSampleTargetsRejectsImpossibleStrideRule(t *testing.T) {
	_, err := SampleTargets(2000, Profile{}, RunRules{
		MinDistanceM: 2000, MaxDistanceM: 3000,
		FastestPaceSecPerKM: 300, SlowestPaceSecPerKM: 360,
		MinCadenceSPM: 150, MaxCadenceSPM: 170,
		MaxSegmentStrideM: .2,
	}, 1)
	if err == nil {
		t.Fatal("expected inconsistent rule error")
	}
}

func TestSampleTargetsDerivesCadenceWhenSchoolLeavesFrequencyDisabled(t *testing.T) {
	selection, err := SampleTargets(2000, Profile{}, RunRules{
		MinDistanceM: 1600, MaxDistanceM: 5000,
		FastestPaceSecPerKM: 210, SlowestPaceSecPerKM: 600,
		MinCadenceSPM: 0, MaxCadenceSPM: 0,
		MaxSegmentStrideM: 1.8,
	}, 99)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Profile.CadenceSPM == 168 {
		t.Fatal("disabled upstream cadence range must not force the old 168 spm placeholder")
	}
	if selection.Profile.CadenceSPM < 145 || selection.Profile.CadenceSPM > 205 {
		t.Fatalf("derived cadence %.1f outside natural running envelope", selection.Profile.CadenceSPM)
	}
	if selection.Profile.TargetStrideM <= 0 || selection.Profile.TargetStrideM >= 1.8 {
		t.Fatalf("derived stride %.3f outside server ceiling", selection.Profile.TargetStrideM)
	}
	derived := 60000 / (selection.Profile.TargetPaceSecPerKM * selection.Profile.CadenceSPM)
	if delta := derived - selection.Profile.TargetStrideM; delta < -0.002 || delta > 0.002 {
		t.Fatalf("stride %.3f is inconsistent with derived %.3f", selection.Profile.TargetStrideM, derived)
	}
}

func TestSampleTargetsForDistanceDefaultsToHighestHundredMetreSlot(t *testing.T) {
	rules := RunRules{
		MinDistanceM: 1650, MaxDistanceM: 5035,
		FastestPaceSecPerKM: 300, SlowestPaceSecPerKM: 480,
		MinCadenceSPM: 150, MaxCadenceSPM: 185,
		MaxSegmentStrideM: 1.8,
	}
	selection, err := SampleTargetsForDistance(2000, Profile{}, rules, 0, 42)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Policy != ManualDistancePolicy || selection.TargetDistanceM != 5000 {
		t.Fatalf("selection=%+v", selection)
	}
	if selection.Window.MinDistanceM != 1700 || selection.Window.MaxDistanceM != 5000 {
		t.Fatalf("distance window=%+v", selection.Window)
	}

	selection, err = SampleTargetsForDistance(2000, Profile{}, rules, 3200, 42)
	if err != nil || selection.TargetDistanceM != 3200 {
		t.Fatalf("manual selection=%+v err=%v", selection, err)
	}
	if _, err := SampleTargetsForDistance(2000, Profile{}, rules, 3250, 42); err == nil {
		t.Fatal("expected non-100m selection to fail")
	}
}

func TestSampleAutomationTargetsUsesLowestSlotForCountPlan(t *testing.T) {
	rules := RunRules{
		PlanType: PlanTypeByTimes, MinDistanceM: 1050, MaxDistanceM: 5000,
		FastestPaceSecPerKM: 240, SlowestPaceSecPerKM: 600,
		MinCadenceSPM: 150, MaxCadenceSPM: 190, MaxSegmentStrideM: 2,
	}
	selection, err := SampleAutomationTargets(5000, Profile{}, rules, 4800, 42)
	if err != nil {
		t.Fatal(err)
	}
	if selection.TargetDistanceM != 1100 {
		t.Fatalf("distance=%.0f, want smallest valid 100m slot 1100", selection.TargetDistanceM)
	}
}

func TestSampleAutomationTargetsKeepsSelectionForDistancePlan(t *testing.T) {
	rules := RunRules{
		PlanType: PlanTypeByDistance, MinDistanceM: 1000, MaxDistanceM: 5000,
		FastestPaceSecPerKM: 240, SlowestPaceSecPerKM: 600,
		MinCadenceSPM: 150, MaxCadenceSPM: 190, MaxSegmentStrideM: 2,
	}
	selection, err := SampleAutomationTargets(5000, Profile{}, rules, 3200, 42)
	if err != nil {
		t.Fatal(err)
	}
	if selection.TargetDistanceM != 3200 {
		t.Fatalf("distance=%.0f, want selected distance 3200", selection.TargetDistanceM)
	}
}
