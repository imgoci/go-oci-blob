package blob

// TransferOption adjusts a single byte-moving call: Pull, PullRange,
// or Push. Settings that apply to every call belong on the Client
// instead. No per-call options exist yet; WithProgress arrives with
// progress support.
type TransferOption func(*transferConfig)

// transferConfig collects the settings a transfer's TransferOptions
// apply. It carries nothing yet.
type transferConfig struct{}

// applyTransferOptions folds opts into a fresh per-call config. It
// exists so Pull, PullRange, and Push carry their final signatures
// before any per-call option lands; it will return the config once
// the config carries settings.
func applyTransferOptions(opts []TransferOption) {
	var cfg transferConfig
	for _, opt := range opts {
		opt(&cfg)
	}
}
