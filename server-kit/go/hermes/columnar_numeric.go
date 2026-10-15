package hermes

import (
	"fmt"
	"strconv"
)

// Build each validity word locally. Store one word after processing its bounded row group.
func buildIntDataFieldVector(field, want string, entries []*recordEntry, rows int) (Vector, error) {
	vector := newInt64Vector(rows)
	for base := 0; base < len(entries); base += 64 {
		var valid uint64
		for row := base; row < min(base+64, len(entries)); row++ {
			value, ok := lookupRecordField(entries[row].record.Data, want)
			if !ok {
				continue
			}
			kind, text, ok := value.ScalarIndex()
			if !ok || (kind != 'i' && kind != 'u') {
				continue
			}
			parsed, err := strconv.ParseInt(text, 10, 64)
			movementRecord(movementParseInput, len(text))
			if err != nil {
				return nil, fmt.Errorf("hermes: failed to parse integer field %q value %q: %w", field, text, err)
			}
			vector.values[row] = parsed
			movementRecord(movementNumericWrite, 8)
			valid |= uint64(1) << uint(row-base)
		}
		if valid != 0 {
			vector.validity.words[base>>6] = valid
			movementRecord(movementBitmapWrite, 8)
		}
	}
	return vector, nil
}

// Preserve float parsing and null behavior while storing each validity word once.
func buildFloatDataFieldVector(field, want string, entries []*recordEntry, rows int) (Vector, error) {
	vector := newFloat64Vector(rows)
	for base := 0; base < len(entries); base += 64 {
		var valid uint64
		for row := base; row < min(base+64, len(entries)); row++ {
			value, ok := lookupRecordField(entries[row].record.Data, want)
			if !ok {
				continue
			}
			kind, text, ok := value.ScalarIndex()
			if !ok || kind != 'f' {
				continue
			}
			parsed, err := parseColumnarFloat(text)
			if err != nil {
				return nil, fmt.Errorf("hermes: failed to parse float field %q value %q: %w", field, text, err)
			}
			vector.values[row] = parsed
			movementRecord(movementNumericWrite, 8)
			valid |= uint64(1) << uint(row-base)
		}
		if valid != 0 {
			vector.validity.words[base>>6] = valid
			movementRecord(movementBitmapWrite, 8)
		}
	}
	return vector, nil
}
