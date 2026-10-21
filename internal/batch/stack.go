package batch

import (
	"encoding/binary"
	"fmt"
)

// splitRows cuts a stacked output on axis 0 into one slice per caller. rows are the callers' batch sizes in batch order.
// The slices share memory with out.Data.
func splitRows(out Tensor, rows []int64) ([][]byte, error) {
	var total int64
	for _, r := range rows {
		total += r
	}
	if len(out.Shape) == 0 || out.Shape[0] != total || total == 0 {
		return nil, fmt.Errorf("output %q does not have the %d stacked rows on dimension 0", out.Name, total)
	}
	parts := make([][]byte, len(rows))
	if out.Datatype == TypeBytes {
		return splitBytesRows(out, rows, total, parts)
	}
	rowBytes := int64(len(out.Data)) / total
	if rowBytes*total != int64(len(out.Data)) {
		return nil, fmt.Errorf("output %q has %d bytes, which do not divide into %d rows", out.Name, len(out.Data), total)
	}
	var offset int64
	for i, r := range rows {
		end := offset + r*rowBytes
		parts[i] = out.Data[offset:end:end]
		offset = end
	}
	return parts, nil
}

// splitBytesRows walks length-prefixed BYTES elements, giving each caller its rows' elements.
func splitBytesRows(out Tensor, rows []int64, total int64, parts [][]byte) ([][]byte, error) {
	count, err := ElementCount(out.Shape)
	if err != nil {
		return nil, fmt.Errorf("output %q: %w", out.Name, err)
	}
	perRow := count / total
	if perRow*total != count {
		return nil, fmt.Errorf("output %q has %d elements, which do not divide into %d rows", out.Name, count, total)
	}
	data := out.Data
	var offset int
	for i, r := range rows {
		start := offset
		for range r * perRow {
			if len(data)-offset < bytesLengthSize {
				return nil, fmt.Errorf("output %q BYTES data is cut short", out.Name)
			}
			n := uint64(binary.LittleEndian.Uint32(data[offset:]))
			offset += bytesLengthSize
			if n > uint64(len(data)-offset) { // #nosec G115 -- offset never passes len(data), so the difference is not negative
				return nil, fmt.Errorf("output %q BYTES element overruns the data", out.Name)
			}
			offset += int(n) // #nosec G115 -- n is at most the bytes left in data
		}
		parts[i] = data[start:offset:offset]
	}
	if offset != len(data) {
		return nil, fmt.Errorf("output %q BYTES data has bytes past its elements", out.Name)
	}
	return parts, nil
}
