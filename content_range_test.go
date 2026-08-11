package blob

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseContentRange(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   contentRange
		wantOK bool
	}{
		{
			name:   "parses the spec form",
			header: "bytes 10-1023/4096",
			want:   contentRange{start: 10, end: 1023, total: 4096},
			wantOK: true,
		},
		{
			name:   "parses a single-byte blob",
			header: "bytes 0-0/1",
			want:   contentRange{start: 0, end: 0, total: 1},
			wantOK: true,
		},
		{
			name:   "treats the range unit case-insensitively",
			header: "BYTES 0-0/1",
			want:   contentRange{start: 0, end: 0, total: 1},
			wantOK: true,
		},
		{name: "rejects an unknown total", header: "bytes 0-1023/*", wantOK: false},
		{name: "rejects a missing header", header: "", wantOK: false},
		{name: "rejects a missing prefix", header: "0-1023/4096", wantOK: false},
		{name: "rejects a negative start", header: "bytes -1-2/3", wantOK: false},
		{name: "rejects a signed number", header: "bytes +0-2/3", wantOK: false},
		{name: "rejects a reversed interval", header: "bytes 2-1/3", wantOK: false},
		{name: "rejects an end equal to total", header: "bytes 0-3/3", wantOK: false},
		{name: "rejects surrounding whitespace", header: "bytes 0-2/3 ", wantOK: false},
		{name: "rejects garbage", header: "bytes lots", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseContentRange(tt.header)

			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseContentRangeTotal(t *testing.T) {
	total, ok := parseContentRangeTotal("bytes 0-4/5")

	assert.True(t, ok)
	assert.Equal(t, int64(5), total)
}
