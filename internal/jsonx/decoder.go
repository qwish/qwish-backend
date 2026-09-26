// Package jsonx contains strict JSON request decoding helpers.
package jsonx

import (
	"io"

	jsonv2 "encoding/json/v2"
)

// Decoder reads one complete JSON value using Go 1.27's stricter JSON
// implementation. It rejects duplicate object names, invalid UTF-8, and
// trailing values while retaining encoding/json v1's case-insensitive field
// matching for compatibility with existing clients.
type Decoder struct {
	reader              io.Reader
	rejectUnknownFields bool
}

// NewDecoder returns a request decoder that reads from reader.
func NewDecoder(reader io.Reader) *Decoder {
	return &Decoder{reader: reader}
}

// DisallowUnknownFields makes Decode reject object members that do not match a
// field in the destination type, matching encoding/json.Decoder's behavior.
func (d *Decoder) DisallowUnknownFields() {
	d.rejectUnknownFields = true
}

// Decode unmarshals exactly one complete JSON value into destination.
func (d *Decoder) Decode(destination any) error {
	opts := []jsonv2.Options{jsonv2.MatchCaseInsensitiveNames(true)}
	if d.rejectUnknownFields {
		opts = append(opts, jsonv2.RejectUnknownMembers(true))
	}
	return jsonv2.UnmarshalRead(d.reader, destination, opts...)
}
