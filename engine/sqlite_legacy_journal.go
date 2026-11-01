package main

import (
	"bufio"
	"bytes"
	encodingbinary "encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

type legacyJournalScan struct {
	CompleteBytes int64
	TailBytes     int64
	Frames        int64
}

// scanLegacyJournal has no writable file handle. An incomplete final frame is
// reported, never repaired/truncated. Callbacks run only for complete valid
// frames, including covered revisions (their CRC/format still need validation).
// Memory is bounded by one historical frame, whose legacy cap is 256 MiB.
func scanLegacyJournal(path string, visit func(streamRecord) error) (legacyJournalScan, error) {
	var scan legacyJournalScan
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return scan, nil
	}
	if err != nil {
		return scan, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return scan, err
	}
	if !info.Mode().IsRegular() {
		return scan, errors.New("legacy journal is not a regular file")
	}
	r := bufio.NewReader(f)
	for {
		var header [8]byte
		_, err := io.ReadFull(r, header[:])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			scan.TailBytes = info.Size() - scan.CompleteBytes
			return scan, nil
		}
		if err != nil {
			return scan, err
		}
		n := encodingbinary.LittleEndian.Uint32(header[:4])
		if n == 0 || n > maxStreamRecordBytes {
			return scan, errors.New("invalid stream journal record size")
		}
		// Check the remaining length BEFORE allocation for a short interrupted tail.
		if int64(n) > info.Size()-scan.CompleteBytes-8 {
			scan.TailBytes = info.Size() - scan.CompleteBytes
			return scan, nil
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return scan, err // Source changed while being read: never call this a safe tail.
		}
		if crc32.ChecksumIEEE(body) != encodingbinary.LittleEndian.Uint32(header[4:]) {
			return scan, errors.New("stream journal checksum mismatch")
		}
		var record streamRecord
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return scan, fmt.Errorf("invalid legacy journal frame: %w", err)
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return scan, errors.New("trailing legacy journal frame content")
		}
		if record.Format != 0 && record.Format != 1 {
			return scan, errors.New("unsupported journal format")
		}
		if err := visit(record); err != nil {
			return scan, err
		}
		scan.CompleteBytes += int64(n) + 8
		scan.Frames++
	}
}

// The consumer commits a complete frame atomically before returning. Revision
// advancement is owned here, so even an all-retired-Tasks transaction occupies its
// historical revision. Covered frames still pass the framing checks above.
func readLegacyJournals(dir string, checkpoint uint64, apply func(streamRecord) error) (uint64, map[string]legacyJournalScan, error) {
	revision := checkpoint
	scans := map[string]legacyJournalScan{}
	for _, name := range []string{"stream.sealed", "stream.journal"} {
		scan, err := scanLegacyJournal(filepath.Join(dir, name), func(record streamRecord) error {
			if record.Revision <= revision {
				return nil
			}
			if record.Revision != revision+1 {
				return fmt.Errorf("stream journal revision gap at %d", record.Revision)
			}
			if record.Format == 1 {
				if len(record.Transaction) == 0 || len(record.Updates) != 0 || record.Usage != nil || record.Analytics != nil {
					return errors.New("invalid transaction frame")
				}
				if err := stripAbandonedTaskPatches(&record); err != nil {
					return err
				}
			} else if len(record.Updates) == 0 && record.Usage == nil && record.Analytics == nil {
				return errors.New("empty stream journal record")
			}
			if err := apply(record); err != nil {
				return err
			}
			revision = record.Revision
			return nil
		})
		scans[name] = scan
		if err != nil {
			return revision, scans, fmt.Errorf("%s: %w", name, err)
		}
	}
	return revision, scans, nil
}
