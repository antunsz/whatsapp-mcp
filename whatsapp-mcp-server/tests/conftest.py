import sqlite3

import pytest

import whatsapp


@pytest.fixture
def db(tmp_path, monkeypatch):
    """A throwaway messages.db with one voice note and one image."""
    path = tmp_path / "messages.db"
    conn = sqlite3.connect(path)
    conn.executescript("""
        CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT, last_message_time TIMESTAMP);
        CREATE TABLE messages (id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP,
                               is_from_me BOOLEAN, media_type TEXT, PRIMARY KEY (id, chat_jid));
        INSERT INTO chats VALUES ('5511@s.whatsapp.net', 'João', '2026-10-12 10:00:00');
        INSERT INTO messages VALUES ('AUDIO1', '5511@s.whatsapp.net', '5511', '', '2026-10-12 10:00:00', 0, 'audio');
        INSERT INTO messages VALUES ('IMG1', '5511@s.whatsapp.net', '5511', '', '2026-10-12 10:01:00', 0, 'image');
    """)
    conn.close()
    monkeypatch.setattr(whatsapp, "MESSAGES_DB_PATH", str(path))
    return path
