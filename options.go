package transact

// IsoLevel is the transaction isolation level. Each adapter maps it to its driver.
type IsoLevel int

const (
	// LevelDefault uses the database default isolation level.
	LevelDefault IsoLevel = iota
	// LevelReadUncommitted allows dirty reads.
	LevelReadUncommitted
	// LevelReadCommitted only reads committed rows.
	LevelReadCommitted
	// LevelRepeatableRead sees the same snapshot for the whole transaction.
	LevelRepeatableRead
	// LevelSerializable is the strictest level. Transactions behave as if run one after another.
	LevelSerializable
)

// Deferrable controls deferrable transactions. Only drivers that support it (pgx) use it.
type Deferrable int

const (
	// DeferrableDefault uses the database default.
	DeferrableDefault Deferrable = iota
	// NotDeferrable disables deferrable mode.
	NotDeferrable
	// Deferred allows the database to delay the transaction when it is read-only and serializable.
	Deferred
)

// Options configures how a transaction starts.
type Options struct {
	// IsoLevel is the isolation level. LevelDefault keeps the database default.
	IsoLevel IsoLevel
	// ReadOnly marks the transaction as read-only when true.
	ReadOnly bool
	// Deferrable sets deferrable mode. Only pgx uses it.
	Deferrable Deferrable
}

// Option changes Options. Pass options to New or Run.
type Option func(*Options)

// WithIsoLevel sets the isolation level.
func WithIsoLevel(l IsoLevel) Option { return func(o *Options) { o.IsoLevel = l } }

// WithReadOnly marks the transaction as read-only.
func WithReadOnly() Option { return func(o *Options) { o.ReadOnly = true } }

// WithDeferrable sets deferrable mode. Only pgx uses it.
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
