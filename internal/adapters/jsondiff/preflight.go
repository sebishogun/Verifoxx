package jsondiff

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

type preflightObject uint8

const (
	preflightRoot preflightObject = iota
	preflightField
	preflightDomainValue
	preflightEvidenceSet
	preflightEvidence
	preflightTransition
)

type preflightValue uint8

const (
	preflightScalar preflightValue = iota
	preflightFields
	preflightValues
	preflightEvidenceSets
	preflightEvidenceRecords
	preflightTransitions
)

type configPreflight struct {
	decoder *json.Decoder
	limits  Limits

	fields          int
	values          int
	evidenceSets    int
	evidenceRecords int
	transitions     int
}

func preflightConfigJSON(source []byte, limits Limits) error {
	if !utf8.Valid(source) {
		return ErrInvalidConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.UseNumber()
	preflight := configPreflight{decoder: decoder, limits: limits}
	if err := preflight.object(preflightRoot); err != nil {
		return ErrInvalidConfig
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidConfig
	}
	if preflight.fields == 0 || preflight.transitions != 16 {
		return ErrInvalidConfig
	}
	return nil
}

func (preflight *configPreflight) object(kind preflightObject) error {
	if err := preflight.delimiter('{'); err != nil {
		return err
	}
	var seen uint16
	for preflight.decoder.More() {
		token, err := preflight.decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return ErrInvalidConfig
		}
		bit, value, ok := preflightKey(kind, key)
		if !ok || seen&bit != 0 {
			return ErrInvalidConfig
		}
		seen |= bit
		if value == preflightScalar {
			if err := preflight.scalar(); err != nil {
				return err
			}
			continue
		}
		if err := preflight.array(value); err != nil {
			return err
		}
	}
	return preflight.delimiter('}')
}

func (preflight *configPreflight) array(kind preflightValue) error {
	if err := preflight.delimiter('['); err != nil {
		return err
	}
	for preflight.decoder.More() {
		var child preflightObject
		switch kind {
		case preflightFields:
			if preflight.fields >= preflight.limits.MaxFields {
				return ErrInvalidConfig
			}
			preflight.fields++
			child = preflightField
		case preflightValues:
			if preflight.values >= preflight.limits.MaxValues {
				return ErrInvalidConfig
			}
			preflight.values++
			child = preflightDomainValue
		case preflightEvidenceSets:
			if preflight.evidenceSets >= preflight.limits.MaxEvidenceSets {
				return ErrInvalidConfig
			}
			preflight.evidenceSets++
			child = preflightEvidenceSet
		case preflightEvidenceRecords:
			if preflight.evidenceRecords >= preflight.limits.MaxEvidenceRecords {
				return ErrInvalidConfig
			}
			preflight.evidenceRecords++
			child = preflightEvidence
		case preflightTransitions:
			if preflight.transitions >= 16 {
				return ErrInvalidConfig
			}
			preflight.transitions++
			child = preflightTransition
		default:
			return ErrInvalidConfig
		}
		if err := preflight.object(child); err != nil {
			return err
		}
	}
	return preflight.delimiter(']')
}

func (preflight *configPreflight) scalar() error {
	token, err := preflight.decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return ErrInvalidConfig
	}
	if _, composite := token.(json.Delim); composite {
		return ErrInvalidConfig
	}
	return nil
}

func (preflight *configPreflight) delimiter(want json.Delim) error {
	token, err := preflight.decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != want {
		return ErrInvalidConfig
	}
	return nil
}

func preflightKey(object preflightObject, key string) (uint16, preflightValue, bool) {
	switch object {
	case preflightRoot:
		switch key {
		case "fields":
			return 1 << 0, preflightFields, true
		case "evidence_sets":
			return 1 << 1, preflightEvidenceSets, true
		case "transitions":
			return 1 << 2, preflightTransitions, true
		case "max_candidates":
			return 1 << 3, preflightScalar, true
		case "batch_rows":
			return 1 << 4, preflightScalar, true
		case "evidence_closed":
			return 1 << 5, preflightScalar, true
		}
	case preflightField:
		switch key {
		case "name":
			return 1 << 0, preflightScalar, true
		case "kind":
			return 1 << 1, preflightScalar, true
		case "group":
			return 1 << 2, preflightScalar, true
		case "values":
			return 1 << 3, preflightValues, true
		case "closed":
			return 1 << 4, preflightScalar, true
		}
	case preflightDomainValue:
		switch key {
		case "state":
			return 1 << 0, preflightScalar, true
		case "string":
			return 1 << 1, preflightScalar, true
		case "integer":
			return 1 << 2, preflightScalar, true
		case "boolean":
			return 1 << 3, preflightScalar, true
		}
	case preflightEvidenceSet:
		if key == "records" {
			return 1, preflightEvidenceRecords, true
		}
	case preflightEvidence:
		switch key {
		case "kind":
			return 1 << 0, preflightScalar, true
		case "state":
			return 1 << 1, preflightScalar, true
		case "subject":
			return 1 << 2, preflightScalar, true
		case "scope":
			return 1 << 3, preflightScalar, true
		case "timing":
			return 1 << 4, preflightScalar, true
		}
	case preflightTransition:
		switch key {
		case "old":
			return 1 << 0, preflightScalar, true
		case "new":
			return 1 << 1, preflightScalar, true
		case "class":
			return 1 << 2, preflightScalar, true
		case "allowed":
			return 1 << 3, preflightScalar, true
		}
	}
	return 0, 0, false
}
