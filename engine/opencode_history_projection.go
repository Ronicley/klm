package main

import (
	"context"
	"errors"
	"io"
	"slices"
)

// These are presentation-only tool diff previews, not tool input/output.
const openCodeDiffPreviewLimit = 64 << 10
const openCodeHistoryWireLimit = 256 << 20
const openCodeOmittedDiff = "[Large diff omitted from the KLM view; preserved in OpenCode.]"

var errOpenCodeHistoryWireLimit = errors.New("history page exceeded KLM's 256 MiB input limit")
var errOpenCodeHistoryJSON = errProtocolJSON

func readOpenCodeHistoryJSON(input io.Reader) ([]byte, error) {
	return readOpenCodeHistoryJSONContext(context.Background(), input)
}

// History has a known array/message envelope, so policy does not depend on an
// event tag. Large summary arrays are drained, not cached in infos/usage maps.
// Whole-page useful-content limits remain explicit until the streaming slice.
func readOpenCodeHistoryJSONContext(ctx context.Context, input io.Reader) ([]byte, error) {
	wire := &io.LimitedReader{R: input, N: openCodeHistoryWireLimit + 1}
	policy := func(path []string) projectionAction {
		if slices.Equal(path, []string{"[]", "info", "summary", "diffs"}) {
			return projectionDiscard
		}
		if openCodeDiffPreviewPath(path) {
			return projectionDiffPreview
		}
		return projectionKeep
	}
	data, _, err := projectProtocolJSON(ctx, wire, policy, openCodeResponseLimit, errOpenCodeResponseTooLarge, false)
	if wire.N == 0 {
		return nil, errOpenCodeHistoryWireLimit
	}
	return data, err
}

func openCodeDiffPreviewPath(path []string) bool {
	if len(path) != 6 && len(path) != 8 {
		return false
	}
	if !slices.Equal(path[:5], []string{"[]", "parts", "[]", "state", "metadata"}) {
		return false
	}
	return len(path) == 6 && path[5] == "diff" ||
		len(path) == 8 && path[5] == "files" && path[6] == "[]" && path[7] == "patch"
}
