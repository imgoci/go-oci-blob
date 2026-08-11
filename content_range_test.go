package blob

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseContentRangeTotal(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		wantTotal int64
		wantOK    bool
	}{
		{name: "parses the spec form", header: "bytes 0-1023/4096", wantTotal: 4096, wantOK: true},
		{name: "parses a single-chunk blob", header: "bytes 0-4/5", wantTotal: 5, wantOK: true},
		{name: "rejects an unknown total", header: "bytes 0-1023/*", wantOK: false},
		{name: "rejects a missing header", header: "", wantOK: false},
		{name: "rejects a missing prefix", header: "0-1023/4096", wantOK: false},
		{name: "rejects garbage", header: "bytes lots", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total, ok := parseContentRangeTotal(tt.header)

			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantTotal, total)
			}
		})
	}
}
