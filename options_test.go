package transact

import "testing"

func TestBuildOptionsLaterOverridesEarlier(t *testing.T) {
	o := buildOptions([]Option{
		WithIsoLevel(LevelSerializable),
		WithReadOnly(),
		WithIsoLevel(LevelReadCommitted),
	})
	if o.IsoLevel != LevelReadCommitted {
		t.Fatalf("IsoLevel = %v, want LevelReadCommitted", o.IsoLevel)
	}
	if !o.ReadOnly {
		t.Fatal("ReadOnly = false, want true")
	}
}
