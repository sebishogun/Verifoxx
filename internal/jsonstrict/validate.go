// Package jsonstrict validates JSON structure before typed decoding.
package jsonstrict

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var ErrInvalid = errors.New("jsonstrict: invalid JSON")

// Validate rejects duplicate object keys, trailing values, and excessive depth.
func Validate(source []byte, maxDepth int) error {
	if len(source) == 0 || maxDepth <= 0 {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.UseNumber()
	if err := validateValue(decoder, 1, maxDepth); err != nil {
		return ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func validateValue(decoder *json.Decoder, depth, maxDepth int) error {
	if depth > maxDepth {
		return ErrInvalid
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make([]string, 0, 8)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalid
			}
			for _, previous := range keys {
				if previous == key {
					return ErrInvalid
				}
			}
			keys = append(keys, key)
			if err := validateValue(decoder, depth+1, maxDepth); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		for decoder.More() {
			if err := validateValue(decoder, depth+1, maxDepth); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
