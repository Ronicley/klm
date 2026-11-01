package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const protocolStagingLimit int64 = 2 << 30
const protocolDiskReserve uint64 = 512 << 20

var protocolStagingBudget struct {
	sync.Mutex
	bytes int64
}

var errProtocolStaging = errors.New("Harness input staging capacity exhausted (2 GiB shared budget or 512 MiB free-disk reserve).")
var errProtocolStagingIO = errors.New("Could not stage harness input in the engine data directory.")

// Called only after acquiring the engine data-directory lock, before starting
// workers. Staging is unpublished transport data, never native/canonical history.
func prepareProtocolStaging(dir string) error {
	path := filepath.Join(dir, "ingestion-staging")
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errProtocolStagingIO
		}
		if err := os.RemoveAll(path); err != nil {
			return errProtocolStagingIO
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errProtocolStagingIO
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return errProtocolStagingIO
	}
	return nil
}

type protocolFrame struct {
	ctx                   context.Context
	dir                   string
	memory                []byte
	file                  *os.File
	size                  int64
	retained              bool
	scope                 *protocolContentScope
	ownerRead             bool
	lease                 bool
	cacheRefs, scopeBytes int
	validated             bool
	nativeType            string
	memoryReserved        int
}

func (f *protocolFrame) Write(data []byte) (int, error) {
	n := len(data)
	for len(data) > 0 {
		if err := f.ctx.Err(); err != nil {
			return 0, err
		}
		part := data[:min(len(data), protocolChunkSize)]
		if err := f.writeChunk(part); err != nil {
			return 0, err
		}
		data = data[len(part):]
	}
	return n, nil
}

func (f *protocolFrame) writeChunk(part []byte) error {
	protocolStagingBudget.Lock()
	defer protocolStagingBudget.Unlock()
	if int64(len(part)) > protocolStagingLimit-protocolStagingBudget.bytes {
		return errProtocolStaging
	}
	if f.file == nil && len(part) <= protocolChunkSize-len(f.memory) {
		if f.memory == nil {
			if err := reserveProtocolResident(protocolChunkSize); err != nil {
				return err
			}
			f.memoryReserved = protocolChunkSize
			f.memory = make([]byte, 0, protocolChunkSize)
		}
		f.memory = append(f.memory, part...)
	} else {
		dir := filepath.Join(f.dir, "ingestion-staging")
		free, err := protocolFreeDisk(dir)
		if err != nil {
			return errProtocolStagingIO
		}
		needed := len(part)
		if f.file == nil {
			needed += len(f.memory)
		}
		if free < protocolDiskReserve+uint64(needed) {
			return errProtocolStaging
		}
		if f.file == nil {
			f.file, err = os.CreateTemp(dir, "frame-*")
			if err != nil {
				return errProtocolStagingIO
			}
			if _, err := f.file.Write(f.memory); err != nil {
				return errProtocolStagingIO
			}
			f.memory = nil
			_ = reserveProtocolResident(-f.memoryReserved)
			f.memoryReserved = 0
		}
		if _, err := f.file.Write(part); err != nil {
			return errProtocolStagingIO
		}
	}
	f.size += int64(len(part))
	protocolStagingBudget.bytes += int64(len(part))
	return nil
}

func (f *protocolFrame) reader() (io.Reader, error) {
	if f.file == nil {
		return bytes.NewReader(f.memory), nil
	}
	if _, err := f.file.Seek(0, io.SeekStart); err != nil {
		return nil, errProtocolStagingIO
	}
	return f.file, nil
}

func (f *protocolFrame) Close() error {
	var err error
	release := f.size
	if f.file != nil {
		name := f.file.Name()
		closeErr := f.file.Close()
		removeErr := os.Remove(name)
		if closeErr != nil || removeErr != nil {
			err = errProtocolStagingIO
		}
		if removeErr != nil {
			// Keep failed cleanup charged until the locked startup sweep. Repeated
			// cleanup failures cannot silently reopen the shared staging budget.
			release = 0
		}
		f.file = nil
	}
	protocolStagingBudget.Lock()
	protocolStagingBudget.bytes -= release
	_ = reserveProtocolResident(-f.memoryReserved)
	f.memoryReserved = 0
	protocolStagingBudget.Unlock()
	f.memory, f.size = nil, 0
	return err
}

// Classification validates/drains the complete frame before choosing a policy.
// Rewindable staging makes field order irrelevant, without retaining an unknown
// giant field or authorizing based on a preview. The second pass remains bounded.
func (f *protocolFrame) project(harness string, limit int) ([]byte, error) {
	r, err := f.reader()
	if err != nil {
		return nil, err
	}
	_, typ, err := projectProtocolJSON(f.ctx, r, nil, 0, nil, true)
	if err == nil && typ == "" {
		err = errProtocolJSON
	}
	if err != nil {
		return nil, protocolFrameError(harness, typ, f.size, err)
	}
	r, err = f.reader()
	if err != nil {
		return nil, err
	}
	limitErr := fmt.Errorf("retained content exceeded the current %d MiB adapter capacity", limit>>20)
	data, _, err := projectProtocolJSON(f.ctx, r, liveProtocolPolicy(harness, typ), limit, limitErr, false)
	if err != nil {
		return nil, protocolFrameError(harness, typ, f.size, err)
	}
	return data, nil
}

func protocolFrameError(harness, typ string, size int64, cause error) error {
	// Native type names are not trusted diagnostics. Only expose known labels.
	switch typ {
	case "message.updated", "message.part.updated", "message.part.delta", "session.diff", "session.error", "session.status",
		"agent_end", "turn_end", "message_end", "message_update", "tool_execution_start", "tool_execution_update", "tool_execution_end", "response", "extension_ui_request", "agent_settled":
	default:
		typ = "unclassified"
	}
	return fmt.Errorf("%s ingestion (type: %s; input bytes: %d): %w", harness, typ, size, cause)
}
