package blob

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRangeAck(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		wantEnd int64
		wantOK  bool
	}{
		{name: "parses the spec form", header: "0-1023", wantEnd: 1023, wantOK: true},
		{name: "tolerates a bytes= prefix", header: "bytes=0-511", wantEnd: 511, wantOK: true},
		{name: "parses a zero end", header: "0-0", wantEnd: 0, wantOK: true},
		{name: "rejects a missing header", header: "", wantOK: false},
		{name: "rejects a non-zero start", header: "512-1023", wantOK: false},
		{name: "rejects a negative end", header: "0--5", wantOK: false},
		{name: "rejects garbage", header: "all of it", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			end, ok := parseRangeAck(tt.header)

			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantEnd, end)
			}
		})
	}
}
