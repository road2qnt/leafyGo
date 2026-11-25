package domain

import "testing"

func TestInheritsBatchScope(t *testing.T) {
	cases := []struct {
		action, mode string
		want         bool
	}{
		{ActionBatchRetry, ModeBatch, true},
		{ActionBatchRevert, ModeBatch, true},
		{ActionRollback, ModeBatch, true},
		{ActionRollback, ModeNormal, false},
		{ActionApply, ModeBatch, false},
		{ActionForward, ModeBatch, false},
		{ActionResolveUncertain, ModeBatch, false},
	}
	for _, c := range cases {
		if got := InheritsBatchScope(c.action, c.mode); got != c.want {
			t.Errorf("InheritsBatchScope(%q, %q) = %v; want %v", c.action, c.mode, got, c.want)
		}
	}
}

func TestTakesOverHold(t *testing.T) {
	for _, a := range []string{ActionBatchRetry, ActionBatchRevert, ActionResolveUncertain} {
		if !TakesOverHold(a) {
			t.Errorf("TakesOverHold(%q) = false; want true", a)
		}
	}
	for _, a := range []string{ActionApply, ActionRollback, ActionForward} {
		if TakesOverHold(a) {
			t.Errorf("TakesOverHold(%q) = true; want false", a)
		}
	}
}

func TestValidActionAndMode(t *testing.T) {
	if !ValidAction(ActionApply) || ValidAction("drop") {
		t.Error("ValidAction wrong")
	}
	if !ValidMode(ModeBatch) || ValidMode("turbo") {
		t.Error("ValidMode wrong")
	}
}
