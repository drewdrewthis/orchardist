#!/usr/bin/env python3
"""
Session search index for Claude Code conversations.

Builds and queries a SQLite FTS5 index over all session JSONL files.
Supports incremental updates (only re-parses changed files).

Usage:
    session-index.py build                        # Build/update the index
    session-index.py search <query> [--limit N]   # Search sessions, output JSON
    session-index.py context <path.jsonl> [--tail N]  # Extract recent messages from a session
"""

import sqlite3
import json
import os
import sys
import glob
import time

DB_PATH = os.path.expanduser("~/.claude/sessions.db")
PROJECTS_DIR = os.path.expanduser("~/.claude/projects")


def get_db():
    db = sqlite3.connect(DB_PATH)
    db.row_factory = sqlite3.Row
    db.execute("PRAGMA journal_mode=WAL")
    db.execute("""
        CREATE TABLE IF NOT EXISTS sessions (
            session_id TEXT PRIMARY KEY,
            project TEXT,
            file_path TEXT,
            mtime REAL,
            message_count INTEGER,
            first_prompt TEXT,
            last_prompt TEXT
        )
    """)
    db.execute("""
        CREATE VIRTUAL TABLE IF NOT EXISTS sessions_fts USING fts5(
            session_id,
            project,
            user_text,
            tokenize='porter unicode61'
        )
    """)
    return db


def extract_user_messages(jsonl_path):
    """Parse a JSONL session file and extract user message text."""
    messages = []
    for line in open(jsonl_path):
        line = line.strip()
        if not line:
            continue
        try:
            obj = json.loads(line)
            if obj.get("type") != "user":
                continue
            content = obj.get("message", {}).get("content", "")
            text = ""
            if isinstance(content, list):
                for c in content:
                    if isinstance(c, dict) and c.get("type") == "text":
                        text += c["text"] + " "
            else:
                text = str(content)
            text = text.strip()
            if len(text) < 10:
                continue
            if text.startswith("Base directory for this skill:"):
                continue
            if text.startswith("<local-command"):
                continue
            messages.append(text)
        except (json.JSONDecodeError, KeyError):
            continue
    return messages


def build_index():
    db = get_db()
    files = glob.glob(os.path.join(PROJECTS_DIR, "*", "*.jsonl"))
    files = [f for f in files if "/subagents/" not in f]

    # Load existing mtimes
    existing = {}
    for row in db.execute("SELECT session_id, mtime FROM sessions"):
        existing[row["session_id"]] = row["mtime"]

    # Track which session IDs are still on disk
    on_disk = set()
    indexed = 0
    skipped = 0
    start = time.time()

    for f in files:
        session_id = os.path.basename(f).replace(".jsonl", "")
        on_disk.add(session_id)
        mtime = os.path.getmtime(f)

        if session_id in existing and existing[session_id] == mtime:
            skipped += 1
            continue

        project = os.path.basename(os.path.dirname(f))
        try:
            messages = extract_user_messages(f)
        except Exception:
            continue

        combined = "\n".join(messages)
        first_prompt = messages[0][:200] if messages else ""
        last_prompt = messages[-1][:200] if len(messages) > 1 else first_prompt

        # Upsert metadata
        db.execute("""
            INSERT INTO sessions (session_id, project, file_path, mtime, message_count, first_prompt, last_prompt)
            VALUES (?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(session_id) DO UPDATE SET
                project=excluded.project, file_path=excluded.file_path,
                mtime=excluded.mtime, message_count=excluded.message_count,
                first_prompt=excluded.first_prompt, last_prompt=excluded.last_prompt
        """, (session_id, project, f, mtime, len(messages), first_prompt, last_prompt))

        # Upsert FTS (delete old entry first)
        db.execute("DELETE FROM sessions_fts WHERE session_id = ?", (session_id,))
        db.execute("INSERT INTO sessions_fts (session_id, project, user_text) VALUES (?, ?, ?)",
                   (session_id, project, combined))

        indexed += 1

    # Remove sessions whose files no longer exist
    removed = 0
    for sid in existing:
        if sid not in on_disk:
            db.execute("DELETE FROM sessions WHERE session_id = ?", (sid,))
            db.execute("DELETE FROM sessions_fts WHERE session_id = ?", (sid,))
            removed += 1

    db.commit()
    elapsed = time.time() - start
    total = db.execute("SELECT COUNT(*) FROM sessions").fetchone()[0]
    print(json.dumps({
        "indexed": indexed,
        "skipped": skipped,
        "removed": removed,
        "total": total,
        "elapsed_seconds": round(elapsed, 2)
    }))


def search(query, limit=10):
    db = get_db()

    # Check if index exists
    total = db.execute("SELECT COUNT(*) FROM sessions").fetchone()[0]
    if total == 0:
        print(json.dumps({"error": "Index is empty. Run: session-index.py build"}))
        sys.exit(1)

    results = []
    rows = db.execute("""
        SELECT
            f.session_id,
            s.project,
            s.file_path,
            s.mtime,
            s.message_count,
            s.first_prompt,
            s.last_prompt,
            snippet(sessions_fts, 2, '>>>', '<<<', '...', 24) as snippet,
            rank
        FROM sessions_fts f
        JOIN sessions s ON s.session_id = f.session_id
        WHERE sessions_fts MATCH ?
        ORDER BY rank
        LIMIT ?
    """, (query, limit)).fetchall()

    for row in rows:
        proj = row["project"].replace("-Users-hope-", "~/").replace("-", "/")
        results.append({
            "session_id": row["session_id"],
            "project": proj,
            "file_path": row["file_path"],
            "mtime": row["mtime"],
            "message_count": row["message_count"],
            "first_prompt": row["first_prompt"],
            "last_prompt": row["last_prompt"],
            "snippet": row["snippet"],
            "score": row["rank"],
        })

    print(json.dumps(results, indent=2))


def context(jsonl_path, tail=10):
    """Extract recent user/assistant messages from a session for context."""
    messages = []
    with open(jsonl_path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
                msg_type = obj.get("type")
                if msg_type not in ("user", "assistant"):
                    continue
                content = obj.get("message", {}).get("content", "")
                text = ""
                if isinstance(content, list):
                    for c in content:
                        if isinstance(c, dict) and c.get("type") == "text":
                            text += c["text"] + " "
                else:
                    text = str(content)
                text = text.strip()
                if not text or len(text) < 10:
                    continue
                if text.startswith("Base directory for this skill:"):
                    continue
                if text.startswith("<local-command"):
                    continue
                idx = text.find("<system-reminder>")
                if idx > 0:
                    text = text[:idx]
                messages.append({"role": msg_type, "text": text[:400]})
            except (json.JSONDecodeError, KeyError):
                continue

    recent = messages[-tail:]
    print(json.dumps(recent, indent=2))


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(1)

    cmd = sys.argv[1]

    if cmd == "build":
        build_index()
    elif cmd == "search":
        if len(sys.argv) < 3:
            print("Usage: session-index.py search <query> [--limit N]")
            sys.exit(1)
        query = sys.argv[2]
        limit = 10
        if "--limit" in sys.argv:
            idx = sys.argv.index("--limit")
            limit = int(sys.argv[idx + 1])
        search(query, limit)
    elif cmd == "context":
        if len(sys.argv) < 3:
            print("Usage: session-index.py context <path.jsonl> [--tail N]")
            sys.exit(1)
        path = sys.argv[2]
        tail = 10
        if "--tail" in sys.argv:
            idx = sys.argv.index("--tail")
            tail = int(sys.argv[idx + 1])
        context(path, tail)
    else:
        print(f"Unknown command: {cmd}")
        sys.exit(1)
