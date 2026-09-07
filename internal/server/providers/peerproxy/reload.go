package peerproxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
)

// reloadOnce performs one config reload cycle: load the federation
// config and, on success, apply it to the provider. A parse error is
// logged and the existing peers are kept — ApplyPeers is intentionally
// NOT called, so applyPeersCount stays put on a broken write. The
// success counters advance only on a clean load+apply.
func (cw *ConfigWatcher) reloadOnce() {
	cfg, err := LoadFederationConfig(cw.path)
	if err != nil {
		// Log at Warn — this is operator-actionable. Include the path
		// and, when available, the exact line:column from a JSON syntax
		// error so the operator can pinpoint the mistake.
		logParseError(cw.logger, cw.path, err)
		return
	}
	cw.applyPeersCount.Add(1)
	if err := cw.provider.ApplyPeers(cfg); err != nil {
		cw.logger.Warn("peerproxy: ApplyPeers error after config reload", "err", err)
	}
	cw.reloadCount.Add(1)
}

// logParseError logs a config parse failure at Warn level. When the
// underlying error is a *json.SyntaxError, the offset is translated to a
// 1-based line:column pair by reading the file and counting newlines —
// giving operators an exact location to fix. For I/O errors or other
// non-syntax failures, only the path and error string are logged.
func logParseError(logger *slog.Logger, path string, err error) {
	var synErr *json.SyntaxError
	if errors.As(err, &synErr) && synErr.Offset > 0 {
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			// Clamp offset to file length — SyntaxError can report len(data)
			// when the EOF itself is the problem.
			offset := synErr.Offset
			if offset > int64(len(data)) {
				offset = int64(len(data))
			}
			line, col := offsetToLineCol(data, offset)
			logger.Warn("peerproxy: config parse error; keeping existing peers",
				"path", path, "line", line, "column", col, "err", err)
			return
		}
	}
	logger.Warn("peerproxy: config reload failed; keeping existing peers",
		"path", path, "err", err)
}

// offsetToLineCol converts a byte offset (1-indexed as json.SyntaxError
// reports) into a 1-based line:column pair. The column is the number of
// bytes after the last newline before the offset (not rune count — keeping
// it simple for operator display).
func offsetToLineCol(data []byte, offset int64) (line, col int) {
	// json.SyntaxError.Offset is 1-indexed; convert to 0-indexed for slicing.
	pos := int(offset) - 1
	if pos < 0 {
		pos = 0
	}
	if pos > len(data) {
		pos = len(data)
	}
	line = 1 + bytes.Count(data[:pos], []byte{'\n'})
	lastNL := bytes.LastIndexByte(data[:pos], '\n')
	col = pos - lastNL // lastNL is -1 when no newline found: col = pos+1 = correct
	return line, col
}
