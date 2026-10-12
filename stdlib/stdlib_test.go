package transactstdlib

import (
	"database/sql"
	"testing"

	"github.com/lrweck/transact"
)

func TestIsolationMapping(t *testing.T) {
	if got := isolation(transact.LevelSerializable); got != sql.LevelSerializable {
		t.Fatalf("isolation = %v, want serializable", got)
	}
	if got := isolation(transact.LevelDefault); got != sql.LevelDefault {
		t.Fatalf("isolation = %v, want default", got)
	}
}
