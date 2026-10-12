package transact

import "testing"

func TestJoinOptionsLaterWins(t *testing.T) {
	o := JoinOptions(
		WithIsoLevel(LevelSerializable),
		WithReadOnly(),
		WithIsoLevel(LevelReadCommitted),
	)
	if o.IsoLevel() != LevelReadCommitted {
		t.Fatalf("IsoLevel = %v, want LevelReadCommitted", o.IsoLevel())
	}
	if !o.ReadOnly() {
		t.Fatal("ReadOnly = false, want true")
	}
}

func TestJoinOptionsExplicitDefaultOverrides(t *testing.T) {
	o := JoinOptions(WithIsoLevel(LevelSerializable), WithIsoLevel(LevelDefault))
	if o.IsoLevel() != LevelDefault {
		t.Fatalf("IsoLevel = %v, want LevelDefault", o.IsoLevel())
	}
}

func TestJoinOptionsEmptyChangesNothing(t *testing.T) {
	o := JoinOptions(WithReadOnly(), WithDeferrable(Deferred), Options{})
	if !o.ReadOnly() || o.Deferrable() != Deferred {
		t.Fatalf("opts = %+v, want read-only + deferred", o)
	}
}

func TestJoinOptionsRetry(t *testing.T) {
	ra := RetryPolicy{MaxAttempts: 2}
	rb := RetryPolicy{MaxAttempts: 5}
	o := JoinOptions(WithRetry(ra), Options{})
	p, ok := o.Retry()
	if !ok || p.MaxAttempts != 2 {
		t.Fatalf("Retry = %+v, %v, want ra", p, ok)
	}
	o = JoinOptions(WithRetry(ra), WithRetry(rb))
	if p, _ := o.Retry(); p.MaxAttempts != 5 {
		t.Fatalf("Retry = %+v, want rb", p)
	}
	if _, ok := (Options{}).Retry(); ok {
		t.Fatal("empty Options should have no retry policy")
	}
}

func TestGettersDefault(t *testing.T) {
	var o Options
	if o.IsoLevel() != LevelDefault || o.ReadOnly() || o.Deferrable() != DeferrableDefault {
		t.Fatalf("zero Options = %+v, want defaults", o)
	}
}
