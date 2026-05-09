package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// PathLookup is the narrow interface the handler uses to resolve a
// sessionUuid to an on-disk path. *claudeprojects.Provider satisfies
// this interface via its PathForSessionUUID method; tests inject stubs.
type PathLookup interface {
	// PathForSessionUUID returns the absolute on-disk path for the
	// conversation identified by uuid. Returns ("", false) when the
	// uuid is not known.
	PathForSessionUUID(ctx context.Context, uuid string) (string, bool)
}

// NewConversationsJSONLHandler returns an http.Handler serving
//
//	GET /v1/conversations/:sessionUuid/jsonl
//
// The handler streams the file via http.ServeContent so Range,
// If-None-Match, and Last-Modified are handled by the standard library.
// A weak ETag (format W/"<size>-<mtime-ns>") is computed from os.Stat
// and set before handing off to ServeContent so the stdlib conditional
// logic applies it.
//
// root is the configured projects root (belt-and-braces path validation
// is added in a later task; the parameter is part of the constructor
// signature now so we do not need to change callers later).
//
// Errors:
//   - 405 for any method other than GET or HEAD.
//   - 404 when the sessionUuid is unknown to lookup.
//   - 404 when the file is not found on disk (fs.ErrNotExist).
//   - 500 for any other OS error.
func NewConversationsJSONLHandler(lookup PathLookup, root string, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &conversationsJSONLHandler{
		lookup: lookup,
		root:   root,
		logger: logger,
	}
}

type conversationsJSONLHandler struct {
	lookup PathLookup
	root   string
	logger *slog.Logger
}

func (h *conversationsJSONLHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only GET and HEAD are supported. http.ServeContent handles HEAD
	// transparently, so we only need to reject other methods here.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse :sessionUuid from the URL path.
	// Expected form: /v1/conversations/<uuid>/jsonl
	uuid, ok := parseSessionUUID(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Resolve uuid → filesystem path via the provider.
	path, ok := h.lookup.PathForSessionUUID(r.Context(), uuid)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Open the file. Map fs.ErrNotExist → 404; everything else → 500.
	f, err := os.Open(path) //nolint:gosec // path comes from the provider, not from user input
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error("conversations jsonl: open failed", "path", path, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	// Stat for size + mtime — both needed for ETag and ServeContent.
	stat, err := f.Stat()
	if err != nil {
		h.logger.Error("conversations jsonl: stat failed", "path", path, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Compute a weak ETag from size and mtime nanoseconds. A weak ETag
	// is appropriate here: two responses with the same size+mtime are
	// semantically equivalent even if we can't guarantee byte-level
	// identity (e.g. if the OS rounds mtime). The format mirrors what
	// many file servers use: W/"<size>-<mtime-ns>".
	etag := fmt.Sprintf(`W/"%d-%d"`, stat.Size(), stat.ModTime().UnixNano())

	// Set Content-Type and ETag BEFORE calling ServeContent. The stdlib
	// will not override a pre-set Content-Type; setting ETag here lets
	// ServeContent use it for If-None-Match conditional GET evaluation.
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("ETag", etag)

	// ServeContent handles Range, If-Modified-Since, If-None-Match,
	// Last-Modified, Content-Length, and 206/304/416 status codes.
	// The empty name ("") prevents the stdlib from sniffing or
	// overriding the Content-Type we set above.
	http.ServeContent(w, r, "", stat.ModTime(), f)
}

// parseSessionUUID extracts the :sessionUuid segment from a path of
// the form /v1/conversations/<uuid>/jsonl. Returns ("", false) when
// the path does not match the expected pattern.
//
// The uuid is treated as an opaque lookup key — it is never joined
// onto the filesystem. Path-traversal characters in the uuid segment
// are harmless because they are only passed to the PathLookup interface
// (which returns a path from its own cache), never used to construct a
// file path directly.
func parseSessionUUID(urlPath string) (string, bool) {
	const prefix = "/v1/conversations/"
	const suffix = "/jsonl"

	rest, ok := strings.CutPrefix(urlPath, prefix)
	if !ok {
		return "", false
	}
	uuid, ok := strings.CutSuffix(rest, suffix)
	if !ok {
		return "", false
	}
	if uuid == "" {
		return "", false
	}
	return uuid, true
}
