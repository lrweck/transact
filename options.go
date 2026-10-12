package transact

import "time"

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

// Options configures Run and Manager. An Options value holds one option or,
// joined, a set of them (see JoinOptions). Each function takes a list of
// options, where properties set in later options override previously set
// ones. Unset properties keep their defaults.
type Options struct {
	isoLevel   *IsoLevel
	readOnly   *bool
	deferrable *Deferrable
	retry      *RetryPolicy
}

// JoinOptions coalesces options into one. Properties set in later options
// override the value of previously set properties. Unset properties stay
// unset, so joining with an empty Options changes nothing.
func JoinOptions(opts ...Options) Options {
	var out Options
	for _, o := range opts {
		if o.isoLevel != nil {
			out.isoLevel = o.isoLevel
		}
		if o.readOnly != nil {
			out.readOnly = o.readOnly
		}
		if o.deferrable != nil {
			out.deferrable = o.deferrable
		}
		if o.retry != nil {
			out.retry = o.retry
		}
	}
	return out
}

// WithIsoLevel sets the isolation level.
func WithIsoLevel(l IsoLevel) Options { return Options{isoLevel: &l} }

// WithReadOnly marks the transaction as read-only.
func WithReadOnly() Options {
	v := true
	return Options{readOnly: &v}
}

// WithDeferrable sets deferrable mode. Only pgx uses it.
func WithDeferrable(d Deferrable) Options { return Options{deferrable: &d} }

// IsoLevel returns the isolation level, or LevelDefault when unset.
func (o Options) IsoLevel() IsoLevel {
	if o.isoLevel == nil {
		return LevelDefault
	}
	return *o.isoLevel
}

// ReadOnly reports whether the transaction is read-only. Unset means false.
func (o Options) ReadOnly() bool {
	return o.readOnly != nil && *o.readOnly
}

// Deferrable returns the deferrable mode, or DeferrableDefault when unset.
func (o Options) Deferrable() Deferrable {
	if o.deferrable == nil {
		return DeferrableDefault
	}
	return *o.deferrable
}

// Retry returns the retry policy and whether one is set.
func (o Options) Retry() (*RetryPolicy, bool) {
	return o.retry, o.retry != nil
}

// RetryPolicy controls retries of a failed transaction.
// Build one with the retry module's New, or fill it by hand.
type RetryPolicy struct {
	// MaxAttempts is the total number of tries, including the first one.
	// Values below 1 mean 1, a single try with no retry.
	MaxAttempts int
	// PerTryTimeout bounds each try's begin and commit. It does not bound
	// the callback: queries use your context. Zero or less means no limit.
	PerTryTimeout time.Duration
	// TotalTimeout bounds the whole operation, all tries together.
	// Zero or less means no limit.
	TotalTimeout time.Duration
	// RetryIf reports whether the error from a failed try is worth
	// retrying. Nil means every error is retried. Returning false stops
	// and returns the error.
	RetryIf func(error) bool
	// Wait returns how long to hold before the next try. n is the number
	// of failed tries so far, starting at 1. Nil means no wait.
	Wait func(failedTries int) time.Duration
}

// WithRetry enables retries with p.
func WithRetry(p RetryPolicy) Options { return Options{retry: &p} }
