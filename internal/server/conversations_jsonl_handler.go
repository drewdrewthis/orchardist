package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
// root is the configured projects root. At construction time the handler
// computes a canonical clean root via filepath.EvalSymlinks (falling back
// to filepath.Clean(filepath.Abs) when the root does not yet exist). Per
// request, any path returned by the PathLookup is validated to be a
// descendant of cleanRoot using filepath.Rel — the classic HasPrefix-bypass
// bug is avoided by design.
//
// Errors:
//   - 405 for any method other than GET or HEAD.
//   - 404 when the sessionUuid is unknown to lookup.
//   - 404 when the resolved path is not a descendant of the projects root.
//   - 404 when the file is not found on disk (fs.ErrNotExist).
//   - 500 for any other OS error.
func NewConversationsJSONLHandler(lookup PathLookup, root string, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	cleanRoot := computeCleanRoot(root, logger)

	return &conversationsJSONLHandler{
		lookup:    lookup,
		root:      root,
		cleanRoot: cleanRoot,
		logger:    logger,
	}
}

type conversationsJSONLHandler struct {
	lookup    PathLookup
	root      string
	cleanRoot string // canonical root for path-traversal validation
	logger    *slog.Logger
}

// computeCleanRoot computes the canonical form of root for path-traversal
// validation. EvalSymlinks is preferred; if the directory does not exist yet
// (fresh install), we fall back to Clean(Abs). A warning is logged in the
// fallback case but the daemon continues to start.
func computeCleanRoot(root string, logger *slog.Logger) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		// filepath.Abs only fails when os.Getwd fails — extremely unusual.
		logger.Warn("conversations jsonl: filepath.Abs failed for root, using raw value",
			"root", root, "err", err)
		return filepath.Clean(root)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Root doesn't exist yet (fresh install) or is a broken symlink.
		logger.Warn("conversations jsonl: EvalSymlinks failed for root, falling back to Clean(Abs)",
			"root", root, "err", err)
		return filepath.Clean(abs)
	}
	return resolved
}

// validatePath checks that candidate resolves to a path that is a descendant
// of cleanRoot. It resolves symlinks on the candidate path (belt-and-braces:
// a symlink inside the root pointing outside is still rejected) and uses
// filepath.Rel to avoid the HasPrefix-bypass bug.
//
// Returns the symlink-resolved path on success, or a non-nil error (which the
// caller maps to 404 — the exact reason is intentionally not exposed to HTTP
// clients). The caller MUST open the returned resolved path, not the input
// candidate, to avoid a TOCTOU window where a symlink is rewritten between
// EvalSymlinks and os.Open.
func validatePath(cleanRoot, candidate string) (string, error) {
	// Step 1: clean without symlink resolution first, so we can use the
	// cleaned path as input to EvalSymlinks.
	cleaned := filepath.Clean(candidate)

	// Step 2: resolve symlinks so a symlink inside root that points outside
	// is caught.
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		// File doesn't exist, is a dangling symlink, or permission denied.
		// Treat as a validation failure (caller will surface as 404).
		return "", fmt.Errorf("EvalSymlinks(%q): %w", cleaned, err)
	}

	// Step 3: use filepath.Rel — NOT strings.HasPrefix — to check descent.
	// HasPrefix has the classic bypass bug: /foo/bar HasPrefix /foo is true,
	// but /foobar HasPrefix /foo is also true if you use string prefix alone.
	// filepath.Rel("/a/b", "/a/bc") → "../bc" (starts with ".."), which we reject.
	rel, err := filepath.Rel(cleanRoot, resolved)
	if err != nil {
		return "", fmt.Errorf("filepath.Rel(%q, %q): %w", cleanRoot, resolved, err)
	}
	// rel starts with ".." when resolved is not under cleanRoot.
	// rel == ".." is the exact-parent case, also rejected.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is not under root %q (rel=%q)", resolved, cleanRoot, rel)
	}
	return resolved, nil
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

	// Belt-and-braces: validate that the resolved path is a descendant of
	// the configured root. This guards against a misbehaving provider and
	// against symlinks that point outside the root. Uses filepath.Rel, not
	// strings.HasPrefix, to avoid the classic prefix-bypass bug. Returns
	// the symlink-resolved path so we open exactly what we validated —
	// closes the TOCTOU window where a symlink could be rewritten between
	// EvalSymlinks and os.Open.
	resolved, err := validatePath(h.cleanRoot, path)
	if err != nil {
		h.logger.Warn("conversations jsonl: path validation rejected candidate",
			"cleanRoot", h.cleanRoot, "candidate", path, "err", err)
		http.NotFound(w, r)
		return
	}

	// Open the symlink-resolved path. Map fs.ErrNotExist → 404; everything else → 500.
	f, err := os.Open(resolved) //nolint:gosec // resolved is validated against cleanRoot above
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
