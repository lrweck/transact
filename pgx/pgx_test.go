package transactpgx

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lweck/transact"
)

func TestTxOptionsMapping(t *testing.T) {
	got := txOptions(transact.Options{
		IsoLevel:   transact.LevelSerializable,
		ReadOnly:   true,
		Deferrable: transact.Deferred,
	})
	if got.IsoLevel != pgx.Serializable || got.AccessMode != pgx.ReadOnly || got.DeferrableMode != pgx.Deferrable {
		t.Fatalf("txOptions = %+v", got)
	}
}
