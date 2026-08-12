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
		if opt == nil {
			continue
		}
		opt(&cfg)
	}
	return cfg
}

// WithProgress reports transfer progress to fn.
//
// fn receives the cumulative number of bytes moved so far and the
// total (-1 when the total is unknown). Pull counts bytes delivered to
// the caller. Monolithic Push reports only after the final 201 response;
// chunked Push advances after each PATCH Range acknowledgement, so reaching
// total does not prove the final commit succeeded. Only a nil Push error does.
// Within one transfer, counts never move backward across retries or resumes,
// and calls to fn do not overlap, including during a parallel Pull. Concurrent
// transfers may call the same fn at the same time, so fn must protect any
// state shared between transfers. fn runs synchronously on the transfer path,
// so it must return quickly.
func WithProgress(fn func(done, total int64)) TransferOption {
	return func(cfg *transferConfig) {
		cfg.progress = fn
	}
}
