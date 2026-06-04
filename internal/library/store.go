package library

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed Library index. Complex fields (audio codecs,
// subtitles) are stored as JSON blobs — fine for the read patterns we have.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS titles (
	content_id     TEXT PRIMARY KEY,
	path           TEXT NOT NULL,
	display_title  TEXT NOT NULL,
	size           INTEGER NOT NULL,
	mod_unix       INTEGER NOT NULL,
	duration_ms    INTEGER NOT NULL,
	container      TEXT NOT NULL,
	video_codec    TEXT NOT NULL,
	audio_codecs   TEXT NOT NULL,
	width          INTEGER NOT NULL,
	height         INTEGER NOT NULL,
	hls_compatible INTEGER NOT NULL,
	subtitles      TEXT NOT NULL,
	added_unix     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_titles_path ON titles(path);
`

// OpenStore opens (creating if needed) the SQLite index at path.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Upsert inserts or replaces a Title by Content ID.
func (s *Store) Upsert(t Title) error {
	audio, _ := json.Marshal(t.AudioCodecs)
	subs, _ := json.Marshal(t.Subtitles)
	_, err := s.db.Exec(`
		INSERT INTO titles
			(content_id, path, display_title, size, mod_unix, duration_ms,
			 container, video_codec, audio_codecs, width, height, hls_compatible, subtitles, added_unix)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(content_id) DO UPDATE SET
			path=excluded.path, display_title=excluded.display_title, size=excluded.size,
			mod_unix=excluded.mod_unix, duration_ms=excluded.duration_ms, container=excluded.container,
			video_codec=excluded.video_codec, audio_codecs=excluded.audio_codecs, width=excluded.width,
			height=excluded.height, hls_compatible=excluded.hls_compatible, subtitles=excluded.subtitles`,
		t.ContentID, t.Path, t.DisplayTitle, t.Size, t.ModUnix, t.DurationMS,
		t.Container, t.VideoCodec, string(audio), t.Width, t.Height, boolToInt(t.HLSCompatible),
		string(subs), t.AddedAt.Unix())
	return err
}

// Get returns the Title with the given Content ID.
func (s *Store) Get(contentID string) (Title, bool, error) {
	return s.queryOne(`WHERE content_id = ?`, contentID)
}

// GetByPath returns the Title indexed at path (for the mtime cache).
func (s *Store) GetByPath(path string) (Title, bool, error) {
	return s.queryOne(`WHERE path = ?`, path)
}

// All returns every indexed Title.
func (s *Store) All() ([]Title, error) {
	rows, err := s.db.Query(selectCols + ` FROM titles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Title
	for rows.Next() {
		t, err := scanTitle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Delete removes a Title by Content ID.
func (s *Store) Delete(contentID string) error {
	_, err := s.db.Exec(`DELETE FROM titles WHERE content_id = ?`, contentID)
	return err
}

const selectCols = `SELECT content_id, path, display_title, size, mod_unix, duration_ms,
	container, video_codec, audio_codecs, width, height, hls_compatible, subtitles, added_unix`

func (s *Store) queryOne(where string, arg any) (Title, bool, error) {
	rows, err := s.db.Query(selectCols+` FROM titles `+where, arg)
	if err != nil {
		return Title{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Title{}, false, rows.Err()
	}
	t, err := scanTitle(rows)
	return t, err == nil, err
}

func scanTitle(rows *sql.Rows) (Title, error) {
	var t Title
	var audioJSON, subsJSON string
	var hls int
	var addedUnix int64
	if err := rows.Scan(&t.ContentID, &t.Path, &t.DisplayTitle, &t.Size, &t.ModUnix, &t.DurationMS,
		&t.Container, &t.VideoCodec, &audioJSON, &t.Width, &t.Height, &hls, &subsJSON, &addedUnix); err != nil {
		return Title{}, err
	}
	t.HLSCompatible = hls != 0
	t.AddedAt = time.Unix(addedUnix, 0)
	_ = json.Unmarshal([]byte(audioJSON), &t.AudioCodecs)
	_ = json.Unmarshal([]byte(subsJSON), &t.Subtitles)
	return t, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
