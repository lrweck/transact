package transact

// IsoLevel is the transaction isolation level. Adapters map it to their driver.
type IsoLevel int

const (
	LevelDefault IsoLevel = iota
	LevelReadUncommitted
	LevelReadCommitted
	LevelRepeatableRead
	LevelSerializable
)

// Deferrable controls deferrable transactions. Only drivers that support it (pgx) use it.
type Deferrable int

const (
	DeferrableDefault Deferrable = iota
	NotDeferrable
	Deferred
)

// Options configures how a transaction is started.
type Options struct {
	IsoLevel   IsoLevel
	ReadOnly   bool
	Deferrable Deferrable
}

// Option mutates Options.
type Option func(*Options)

func WithIsoLevel(l IsoLevel) Option { return func(o *Options) { o.IsoLevel = l } }
func WithReadOnly() Option           { return func(o *Options) { o.ReadOnly = true } }
func WithDeferrable(d Deferrable) Option {
	return func(o *Options) { o.Deferrable = d }
}

func buildOptions(opts []Option) Options {
	var o Options
	for _, fn := range opts {
		fn(&o)
	}
	return o
}
