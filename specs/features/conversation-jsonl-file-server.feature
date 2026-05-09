Feature: Daemon file-server endpoint for Conversation jsonl transcripts
  As a client (GUI, future remote orchardists, CLI) of the orchard daemon
  I want a metadata-only `Conversation.jsonlPath` field plus a sibling
  `GET /v1/conversations/:sessionUuid/jsonl` HTTP file-server endpoint
  So that I can stream Claude Code transcript bodies via standard HTTP
  semantics (Range, ETag, If-None-Match) without violating the GraphQL
  schema's "no heavy fields on Conversation" invariant.

  # Issue #505. Read-only v1; auth, federation, push tailing, and mutations
  # are explicit out-of-scope (see issue body Caveats).
  #
  # Two surfaces:
  #   (1) GraphQL: Conversation.jsonlPath: String!  -- absolute path on daemon host
  #   (2) HTTP:    GET /v1/conversations/:sessionUuid/jsonl on the same listener
  #
  # The HTTP endpoint MUST mount on the same `*http.ServeMux` as `/graphql`
  # so it inherits the daemon's loopback bind address and any future auth.

  Background:
    Given the orchard daemon is running on 127.0.0.1:7777
    And the daemon serves `/graphql` from a single `*http.ServeMux`
    And the conversations provider has discovered Claude Code session jsonl files under the projects root
    And each discovered conversation has a stable sessionUuid keyed to its on-disk jsonl path

  # =======================================================================
  # AC1 — Conversation.jsonlPath returns a non-empty absolute path that
  # resolves to a readable file on the daemon's host.
  # =======================================================================

  @unit
  Scenario: jsonlPath field is declared on the Conversation GraphQL type as non-null String
    Given the generated GraphQL schema
    When the Conversation.jsonlPath field is inspected
    Then the field exists on type Conversation
    And its type is String!
    And it carries a doc comment naming the corresponding HTTP endpoint pattern

  @unit
  Scenario: jsonlPath resolver maps an in-memory Conversation to its on-disk path
    Given a discovered Conversation whose internal Path field is "/Users/alice/.claude/projects/foo/bar.jsonl"
    When ToGraphQL projects the Conversation to its GraphQL representation
    Then the resulting jsonlPath equals "/Users/alice/.claude/projects/foo/bar.jsonl"

  @integration
  Scenario: { conversations { sessionUuid jsonlPath } } returns a non-empty absolute path for every conversation
    Given the conversations provider has discovered N conversations under the configured projects root
    When a GraphQL query selects `{ conversations { sessionUuid jsonlPath } }`
    Then the response contains N entries
    And every jsonlPath is a non-empty string
    And every jsonlPath is an absolute path
    And every jsonlPath resolves to a readable regular file on the daemon's host

  # =======================================================================
  # AC2 — Bare GET (no Range) returns the full file with the right headers.
  # =======================================================================

  @integration
  Scenario: GET /v1/conversations/:sessionUuid/jsonl with no Range returns 200 + full body + headers
    Given a known conversation with sessionUuid "9f8e-uuid-1" backed by an on-disk jsonl of 4321 bytes
    When a client issues `GET http://127.0.0.1:7777/v1/conversations/9f8e-uuid-1/jsonl` with no Range header
    Then the response status is 200 OK
    And the response body is the full file (4321 bytes, byte-identical to disk)
    And the `Content-Type` header equals "application/x-ndjson"
    And the response carries an `ETag` header
    And the response carries a `Last-Modified` header

  @unit
  Scenario: ETag is stable across reads when the file is unchanged
    Given a known conversation file that has not changed
    When two GET requests with no Range header are made back-to-back
    Then both responses carry the same ETag value

  @unit
  Scenario: ETag changes when the underlying file changes
    Given a known conversation file
    When the file is appended to (size and/or mtime change)
    And a new GET with no Range header is made
    Then the new response carries a different ETag from the previous response

  # =======================================================================
  # AC3 — Range support: 206 Partial Content for bytes=N- and 416 for OOR.
  # =======================================================================

  @integration
  Scenario: GET with Range bytes=N- returns 206 Partial Content with bytes from N to EOF
    Given a known conversation file of 4321 bytes
    When a client issues `GET ...` with `Range: bytes=2000-`
    Then the response status is 206 Partial Content
    And the response body is exactly bytes [2000, 4321) of the on-disk file
    And the response carries a `Content-Range: bytes 2000-4320/4321` header
    And the `Content-Length` header equals 2321

  @integration
  Scenario: GET with Range bytes=A-B returns 206 with the closed-interval slice
    Given a known conversation file of 4321 bytes
    When a client issues `GET ...` with `Range: bytes=100-199`
    Then the response status is 206 Partial Content
    And the response body is exactly bytes [100, 200) of the on-disk file (100 bytes)
    And the `Content-Range` header reflects "bytes 100-199/4321"

  @integration
  Scenario: GET with an out-of-range Range returns 416 Range Not Satisfiable
    Given a known conversation file of 4321 bytes
    When a client issues `GET ...` with `Range: bytes=99999-`
    Then the response status is 416 Range Not Satisfiable
    And the response carries a `Content-Range: bytes */4321` header

  # =======================================================================
  # AC4 — Conditional GET: If-None-Match returns 304 when ETag matches.
  # =======================================================================

  @integration
  Scenario: GET with If-None-Match matching the current ETag returns 304 Not Modified
    Given a prior GET returned ETag `"<etag-1>"` for a known conversation
    And the file has not been modified since
    When a client issues `GET ...` with header `If-None-Match: "<etag-1>"`
    Then the response status is 304 Not Modified
    And the response body is empty

  @integration
  Scenario: GET with If-None-Match that no longer matches returns 200 with the full body
    Given a prior GET returned ETag `"<etag-1>"` for a known conversation
    And the file has since been appended to (ETag would change)
    When a client issues `GET ...` with header `If-None-Match: "<etag-1>"`
    Then the response status is 200 OK
    And the response body is the current full file

  # =======================================================================
  # AC5 — Unknown sessionUuid returns 404 (not 500, not 200-empty).
  # =======================================================================

  @integration
  Scenario: GET /v1/conversations/<unknown-uuid>/jsonl returns 404 Not Found
    Given the conversations provider has no record of sessionUuid "does-not-exist"
    When a client issues `GET http://127.0.0.1:7777/v1/conversations/does-not-exist/jsonl`
    Then the response status is 404 Not Found
    And the response status is NOT 500
    And the response status is NOT 200

  @integration
  Scenario: GET for a known sessionUuid whose backing file disappeared returns 404
    Given a sessionUuid the provider knew about, whose jsonl file has since been deleted from disk
    When a client issues `GET ...` for that sessionUuid
    Then the response status is 404 Not Found
    And the response status is NOT 500

  # =======================================================================
  # AC6 — Path-traversal defence: the URL carries a sessionUuid, never a
  # filesystem path. Encoded `..` segments must not escape the projects
  # root and must not reach unrelated files.
  # =======================================================================

  @integration
  Scenario: Encoded path-traversal segment in the sessionUuid does not escape the projects root
    Given the projects root is "/Users/alice/.claude/projects"
    When a client issues `GET http://127.0.0.1:7777/v1/conversations/..%2F..%2Fetc%2Fpasswd/jsonl`
    Then the response status is 404 Not Found
    And no file outside the projects root is opened by the daemon
    And the response body does not contain `/etc/passwd` content

  @unit
  Scenario: Handler refuses any resolved path that is not a descendant of the projects root
    Given a sessionUuid whose hypothetical resolved path is outside the projects root after symlink evaluation
    When the handler resolves and validates the path
    Then the handler returns 404 Not Found
    And the handler does not call os.Open on the rejected path

  @unit
  Scenario: Handler does not accept filesystem paths in the URL — only sessionUuid lookup
    Given a request URL where the {sessionUuid} segment contains slashes or `..`
    When the handler parses the path parameter
    Then the parameter is treated as an opaque key for provider lookup
    And the handler does NOT join the parameter onto the projects root

  # =======================================================================
  # AC7 — Endpoint is bound to the same loopback listener as /graphql.
  # =======================================================================

  @integration
  Scenario: The jsonl endpoint is mounted on the same listener as /graphql
    Given the daemon listens on 127.0.0.1:7777 and serves /graphql there
    When the daemon registers its routes
    Then `GET /v1/conversations/{sessionUuid}/jsonl` is reachable at 127.0.0.1:7777
    And no second listener / second port is opened by the daemon for this endpoint

  @unit
  Scenario: The handler is registered on the same *http.ServeMux as /graphql
    Given the server constructs its `*http.ServeMux`
    When routes are registered
    Then both `/graphql` and `/v1/conversations/` are registered on the same mux instance

  # =======================================================================
  # AC8 — Tests assert each AC; streaming path proves no full-file slurp.
  # =======================================================================

  @integration
  Scenario: A 5+ MB jsonl fixture serves a Range read without loading the full file into memory
    Given a real jsonl fixture of at least 5 MiB on disk for sessionUuid "big-uuid-1"
    When a client issues `GET ...` with `Range: bytes=4194304-` (4 MiB offset)
    Then the response status is 206 Partial Content
    And the body matches the corresponding byte range of the fixture
    And process resident-set growth across the request is bounded well below the file size
    And no code path reads the whole file into a single in-memory buffer

  @unit
  Scenario: Each AC of the issue maps to at least one test in the daemon's test suite
    Given the daemon test suite for the file-server endpoint
    When the suite is enumerated
    Then each of AC1..AC7 has at least one assertion in the suite
    And the streaming assertion above (5+ MiB Range read) covers AC8

  # =======================================================================
  # AC9 — Schema doc on Conversation.jsonlPath names the HTTP endpoint, so
  # `__schema` introspection alone is enough for clients to discover it.
  # =======================================================================

  @unit
  Scenario: Conversation.jsonlPath doc string mentions the sibling HTTP endpoint
    Given the generated GraphQL schema
    When `__schema` introspection is queried for the Conversation.jsonlPath description
    Then the description mentions the path pattern `/v1/conversations/:sessionUuid/jsonl`
    And the description notes that the endpoint is hosted on the same daemon listener as `/graphql`

  @unit
  Scenario: schema.graphql source-of-truth carries the same doc text
    Given the source-of-truth `schema.graphql`
    When the Conversation.jsonlPath declaration is inspected
    Then its doc comment mentions the HTTP endpoint pattern
    And the gqlgen-generated description matches that source text

  # =======================================================================
  # End-to-end — full round trip from GraphQL discovery to HTTP body fetch.
  # =======================================================================

  @e2e
  Scenario: Client discovers conversations via GraphQL, then tails one via HTTP Range
    Given the daemon is running on 127.0.0.1:7777
    And the conversations provider has discovered at least one conversation with a non-trivial jsonl
    When a client first queries `{ conversations { sessionUuid jsonlPath } }`
    And the client picks one conversation and issues `GET /v1/conversations/<sessionUuid>/jsonl`
    Then the GET returns 200 OK with `Content-Type: application/x-ndjson` and the full file body
    And a follow-up GET with `Range: bytes=<full-size>-` returns 206 with an empty body up to EOF (or 416 if the offset equals size, per stdlib semantics)
    And after the file is appended to, a follow-up GET with `Range: bytes=<previous-size>-` returns 206 with only the appended bytes

  # --- AC Coverage Map ---
  # AC1 "{ conversations { sessionUuid jsonlPath } } returns non-empty absolute path; resolves to readable file"
  #   -> @unit        "jsonlPath field is declared on the Conversation GraphQL type as non-null String"
  #   -> @unit        "jsonlPath resolver maps an in-memory Conversation to its on-disk path"
  #   -> @integration "{ conversations { sessionUuid jsonlPath } } returns a non-empty absolute path for every conversation"
  #   -> @e2e         "Client discovers conversations via GraphQL, then tails one via HTTP Range"
  #
  # AC2 "GET ... with no Range returns full file with Content-Type application/x-ndjson, ETag, Last-Modified"
  #   -> @integration "GET /v1/conversations/:sessionUuid/jsonl with no Range returns 200 + full body + headers"
  #   -> @unit        "ETag is stable across reads when the file is unchanged"
  #   -> @unit        "ETag changes when the underlying file changes"
  #   -> @e2e         "Client discovers conversations via GraphQL, then tails one via HTTP Range"
  #
  # AC3 "GET ... with Range: bytes=N- returns 206 with bytes from N to EOF; out-of-range returns 416"
  #   -> @integration "GET with Range bytes=N- returns 206 Partial Content with bytes from N to EOF"
  #   -> @integration "GET with Range bytes=A-B returns 206 with the closed-interval slice"
  #   -> @integration "GET with an out-of-range Range returns 416 Range Not Satisfiable"
  #
  # AC4 "GET ... with If-None-Match: <etag> returns 304 when the file is unchanged"
  #   -> @integration "GET with If-None-Match matching the current ETag returns 304 Not Modified"
  #   -> @integration "GET with If-None-Match that no longer matches returns 200 with the full body"
  #
  # AC5 "GET /v1/conversations/<unknown-uuid>/jsonl returns 404 (not 500, not 200-empty)"
  #   -> @integration "GET /v1/conversations/<unknown-uuid>/jsonl returns 404 Not Found"
  #   -> @integration "GET for a known sessionUuid whose backing file disappeared returns 404"
  #
  # AC6 "Refuses to serve files outside the conversations root (path traversal); ..%2F..%2Fetc%2Fpasswd returns 404"
  #   -> @integration "Encoded path-traversal segment in the sessionUuid does not escape the projects root"
  #   -> @unit        "Handler refuses any resolved path that is not a descendant of the projects root"
  #   -> @unit        "Handler does not accept filesystem paths in the URL — only sessionUuid lookup"
  #
  # AC7 "Endpoint is bound to the same loopback listener as /graphql; not on a different port"
  #   -> @integration "The jsonl endpoint is mounted on the same listener as /graphql"
  #   -> @unit        "The handler is registered on the same *http.ServeMux as /graphql"
  #
  # AC8 "A unit/integration test asserts each AC; streaming path exercised against 5+ MiB fixture with Range to prove no full-file slurp"
  #   -> @integration "A 5+ MB jsonl fixture serves a Range read without loading the full file into memory"
  #   -> @unit        "Each AC of the issue maps to at least one test in the daemon's test suite"
  #
  # AC9 "Schema doc on Conversation.jsonlPath mentions the corresponding HTTP endpoint so clients discover it from __schema introspection"
  #   -> @unit        "Conversation.jsonlPath doc string mentions the sibling HTTP endpoint"
  #   -> @unit        "schema.graphql source-of-truth carries the same doc text"
  #
  # Out of scope (per issue Caveats; not covered here):
  #   - Federation: peer hosts serving each other's files. v1 serves only local files.
  #   - Mutations: append/delete/lifecycle. Read-only by design.
  #   - Push/SSE/WS for tailing. Polling Range against the same endpoint covers v1.
  #   - Auth: same posture as /graphql today (loopback-only, no token). Inherits whatever
  #     /graphql gains later.
