package service

import (
	"errors"
	"testing"
)

func TestParseByteRange(t *testing.T) {
	tests := []struct {
		name   string
		header string
		size   int64
		want   *ByteRange
		err    bool
	}{
		{name: "no header", header: "", size: 1000},
		{name: "first bytes", header: "bytes=0-99", size: 1000, want: &ByteRange{Start: 0, End: 99}},
		{name: "open end", header: "bytes=900-", size: 1000, want: &ByteRange{Start: 900, End: 999}},
		{name: "suffix", header: "bytes=-50", size: 1000, want: &ByteRange{Start: 950, End: 999}},
		{name: "suffix longer than file", header: "bytes=-5000", size: 1000, want: &ByteRange{Start: 0, End: 999}},
		{name: "end clamped", header: "bytes=990-5000", size: 1000, want: &ByteRange{Start: 990, End: 999}},
		{name: "single byte", header: "bytes=10-10", size: 1000, want: &ByteRange{Start: 10, End: 10}},
		{name: "spaces around spec", header: " bytes= 1-2 ", size: 1000, want: &ByteRange{Start: 1, End: 2}},
		{name: "1-byte file whole", header: "bytes=0-0", size: 1, want: &ByteRange{Start: 0, End: 0}},
		{name: "1-byte file open end", header: "bytes=0-", size: 1, want: &ByteRange{Start: 0, End: 0}},
		{name: "1-byte file suffix", header: "bytes=-1", size: 1, want: &ByteRange{Start: 0, End: 0}},
		{name: "1-byte file past end", header: "bytes=1-", size: 1, err: true},
		{name: "start past end", header: "bytes=1000-1100", size: 1000, err: true},
		{name: "zero suffix", header: "bytes=-0", size: 1000, err: true},
		{name: "empty spec", header: "bytes=", size: 1000},
		{name: "dash only", header: "bytes=-", size: 1000},
		{name: "plus sign start", header: "bytes=+5-9", size: 1000},
		{name: "plus sign end", header: "bytes=5-+9", size: 1000},
		{name: "plus sign suffix", header: "bytes=-+5", size: 1000},
		{name: "negative start", header: "bytes=-5-9", size: 1000},
		{name: "start overflows int64", header: "bytes=9223372036854775808-", size: 1000},
		{name: "end overflows int64", header: "bytes=0-99999999999999999999", size: 1000},
		{name: "suffix overflows int64", header: "bytes=-99999999999999999999", size: 1000},
		{name: "inverted", header: "bytes=9-0", size: 1000},
		{name: "multi-range", header: "bytes=0-1,5-9", size: 1000},
		{name: "other unit", header: "items=0-9", size: 1000},
		{name: "no dash", header: "bytes=5", size: 1000},
		{name: "letters", header: "bytes=a-b", size: 1000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseByteRange(test.header, test.size)
			if test.err {
				var rangeError RangeNotSatisfiableError
				if !errors.As(err, &rangeError) || rangeError.Size != test.size {
					t.Fatalf("ParseByteRange(%q) error = %v, want RangeNotSatisfiableError{Size: %d}", test.header, err, test.size)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseByteRange(%q) error = %v", test.header, err)
			}
			if (got == nil) != (test.want == nil) || (got != nil && *got != *test.want) {
				t.Errorf("ParseByteRange(%q) = %+v, want %+v", test.header, got, test.want)
			}
		})
	}
}
