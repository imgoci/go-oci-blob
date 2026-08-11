package blob

// TransferOption adjusts a single byte-moving call: Pull, PullRange,
// or Push. Settings that apply to every call belong on the Client
// instead.
type TransferOption func(*transferConfig)

// transferConfig collects the settings a transfer's TransferOptions
// apply.
type transferConfig struct {
	// progress receives cumulative transfer counts; nil reports
	// nothing.
	progress func(done, total int64)
}

// applyTransferOptions folds opts into a fresh per-call config.
func applyTransferOptions(opts []TransferOption) transferConfig {
	var cfg transferConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithProgress reports transfer progress to fn.
//
// fn receives the cumulative number of bytes moved so far and the
// total (-1 when the total is unknown). The count is committed
// progress and never moves backward: a retried request, a resumed
// download, or a restarted upload does not double-count bytes, and
// parallel pulls report one aggregated count. fn runs synchronously
// on the transfer path — possibly from several goroutines during a
// parallel pull, though never concurrently with itself — so it must
// return quickly.
func WithProgress(fn func(done, total int64)) TransferOption {
	return func(cfg *transferConfig) {
		cfg.progress = fn
	}
}
