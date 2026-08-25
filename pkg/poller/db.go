package poller

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// DBManager handles application-specific data stored alongside whatsmeow's data.
type DBManager struct {
	db                *sql.DB
	trustedEmailsPath string
	phraseMu          sync.RWMutex
	phraseCache       map[string][]string
}

type Person struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`

	UnusedAudios int      `json:"unused_audios,omitempty"`
	UsedAudios   int      `json:"used_audios,omitempty"`
	Phones       []string `json:"phones,omitempty"`
}

type SenderSession struct {
	Phone      string `json:"phone"`
	DBFilename string `json:"db_filename"`
	PersonID   *int64 `json:"person_id,omitempty"`
	PersonName string `json:"person_name,omitempty"`
	PersonSlug string `json:"person_slug,omitempty"`
}

func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteRune('-')
			}
		}
	}
	res := strings.Trim(b.String(), "-")
	if res == "" {
		res = "person"
	}
	return res
}

func NewDBManager(dbPath string) (*DBManager, error) {
	dsn := "file:" + dbPath
	if strings.Contains(dbPath, "?") {
		dsn += "&_foreign_keys=on"
	} else {
		dsn += "?_foreign_keys=on"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;"); err != nil {
		return nil, fmt.Errorf("failed to set pragmas: %w", err)
	}
	if err := initSchema(db); err != nil {
		return nil, err
	}

	return &DBManager{
		db:                db,
		trustedEmailsPath: TrustedEmailsFilePath(dbPath),
	}, nil
}

func TrustedEmailsFilePath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "trusted-emails.txt")
}

func initSchema(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS auth_tokens (
			token TEXT PRIMARY KEY,
			email TEXT NOT NULL,
			expires_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS auth_sessions (
			token TEXT PRIMARY KEY,
			email TEXT NOT NULL,
			expires_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS played_songs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			artist TEXT NOT NULL,
			title TEXT NOT NULL,
			played_datetime TEXT NOT NULL,
			UNIQUE(artist, title, played_datetime)
		);`,
		`CREATE TABLE IF NOT EXISTS radio_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			artist TEXT NOT NULL,
			title TEXT NOT NULL,
			played_datetime TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS daily_schedule (
			date TEXT PRIMARY KEY,
			target_matches TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS used_audio_hashes (
			content_hash TEXT PRIMARY KEY,
			first_seen_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS campaign_send_state (
			campaign_artist TEXT PRIMARY KEY,
			last_sent_radio_log_id INTEGER NOT NULL,
			last_sent_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS signature_files (
			bucket TEXT NOT NULL,
			filename TEXT NOT NULL,
			recorded_at TEXT NOT NULL,
			campaign_artist TEXT NOT NULL,
			transcript TEXT DEFAULT '',
			PRIMARY KEY(bucket, filename)
		);`,
		`CREATE TABLE IF NOT EXISTS campaign_phrases (
			campaign_artist TEXT NOT NULL,
			phrase TEXT NOT NULL,
			PRIMARY KEY(campaign_artist, phrase)
		);`,
		`CREATE TABLE IF NOT EXISTS persons (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS sender_sessions (
			phone TEXT PRIMARY KEY,
			db_filename TEXT NOT NULL,
			person_id INTEGER REFERENCES persons(id) ON DELETE SET NULL
		);`,
		`CREATE TABLE IF NOT EXISTS alerts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			level TEXT NOT NULL,
			title TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);`,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			return err
		}
	}

	// Migrations
	_, _ = db.Exec(`ALTER TABLE signature_files ADD COLUMN transcript TEXT DEFAULT '';`)
	_, _ = db.Exec(`ALTER TABLE signature_files ADD COLUMN tags TEXT DEFAULT '';`)
	_, _ = db.Exec(`ALTER TABLE played_songs RENAME COLUMN played_date TO played_datetime;`)
	_, _ = db.Exec(`ALTER TABLE sender_sessions ADD COLUMN person_id INTEGER REFERENCES persons(id) ON DELETE SET NULL;`)

	return nil
}

func (m *DBManager) CreatePerson(ctx context.Context, name string) (*Person, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("person name cannot be empty")
	}
	slug := Slugify(name)
	baseSlug := slug
	counter := 1
	for {
		var exists bool
		err := m.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM persons WHERE slug = ?)", slug).Scan(&exists)
		if err != nil {
			return nil, err
		}
		if !exists {
			break
		}
		counter++
		slug = fmt.Sprintf("%s-%d", baseSlug, counter)
	}

	now := time.Now().UTC()
	res, err := m.db.ExecContext(ctx, "INSERT INTO persons (name, slug, created_at) VALUES (?, ?, ?)", name, slug, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Person{ID: id, Name: name, Slug: slug, CreatedAt: now}, nil
}

func (m *DBManager) ListPersons(ctx context.Context) ([]Person, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT id, name, slug, created_at FROM persons ORDER BY name ASC")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var persons []Person
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, err
		}
		persons = append(persons, p)
	}
	return persons, rows.Err()
}

func (m *DBManager) GetPerson(ctx context.Context, id int64) (*Person, error) {
	p, err := scanPerson(m.db.QueryRowContext(ctx, "SELECT id, name, slug, created_at FROM persons WHERE id = ?", id))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (m *DBManager) GetPersonBySlug(ctx context.Context, slug string) (*Person, error) {
	p, err := scanPerson(m.db.QueryRowContext(ctx, "SELECT id, name, slug, created_at FROM persons WHERE slug = ?", slug))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPerson(row rowScanner) (Person, error) {
	var p Person
	var createdAt any
	if err := row.Scan(&p.ID, &p.Name, &p.Slug, &createdAt); err != nil {
		return Person{}, err
	}
	switch v := createdAt.(type) {
	case time.Time:
		p.CreatedAt = v
	case string:
		for _, layout := range []string{"2006-01-02 15:04:05.999999999 -0700 MST", time.RFC3339} {
			if parsed, err := time.Parse(layout, v); err == nil {
				p.CreatedAt = parsed
				break
			}
		}
	}
	return p, nil
}

func (m *DBManager) UpdatePerson(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("person name cannot be empty")
	}
	_, err := m.db.ExecContext(ctx, "UPDATE persons SET name = ? WHERE id = ?", name, id)
	return err
}

func (m *DBManager) DeletePerson(ctx context.Context, id int64) error {
	_, err := m.db.ExecContext(ctx, "DELETE FROM persons WHERE id = ?", id)
	return err
}

func (m *DBManager) AssignPhoneToPerson(ctx context.Context, phone string, personID *int64) error {
	result, err := m.db.ExecContext(ctx, "UPDATE sender_sessions SET person_id = ? WHERE phone = ?", personID, phone)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return fmt.Errorf("sender phone %s does not exist", phone)
	}
	return nil
}

func (m *DBManager) SetSenderSession(ctx context.Context, phone, dbFilename string) error {
	return m.SetSenderSessionWithPerson(ctx, phone, dbFilename, nil)
}

func (m *DBManager) SetSenderSessionWithPerson(ctx context.Context, phone, dbFilename string, personID *int64) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO sender_sessions (phone, db_filename, person_id) VALUES (?, ?, ?)
		 ON CONFLICT(phone) DO UPDATE SET db_filename = excluded.db_filename,
		 person_id = COALESCE(excluded.person_id, sender_sessions.person_id)`,
		phone, dbFilename, personID,
	)
	return err
}

func (m *DBManager) RemoveSenderSession(ctx context.Context, phone string) error {
	_, err := m.db.ExecContext(ctx, "DELETE FROM sender_sessions WHERE phone = ?", phone)
	return err
}

func (m *DBManager) SenderSessions(ctx context.Context) ([]SenderSession, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT s.phone, s.db_filename, s.person_id, COALESCE(p.name, ''), COALESCE(p.slug, '')
		FROM sender_sessions s
		LEFT JOIN persons p ON s.person_id = p.id
		ORDER BY s.phone
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sessions []SenderSession
	for rows.Next() {
		var session SenderSession
		var pID sql.NullInt64
		if err := rows.Scan(&session.Phone, &session.DBFilename, &pID, &session.PersonName, &session.PersonSlug); err != nil {
			return nil, err
		}
		if pID.Valid {
			idVal := pID.Int64
			session.PersonID = &idVal
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (m *DBManager) IsTrustedEmail(ctx context.Context, email string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false, nil
	}

	data, err := os.ReadFile(m.trustedEmailsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create the file empty if it doesn't exist
			_ = os.WriteFile(m.trustedEmailsPath, []byte(""), 0o644)
			return false, nil
		}
		return false, fmt.Errorf("failed to read trusted emails file: %w", err)
	}

	lines := strings.SplitSeq(string(data), "\n")
	for line := range lines {
		line = strings.ToLower(strings.TrimSpace(line))
		if line == email {
			return true, nil
		}
	}

	return false, nil
}

func (m *DBManager) LogRadioSong(ctx context.Context, artist, title string, date time.Time) (int64, error) {
	dateStr := date.Format("2006-01-02 15:04:05")
	result, err := m.db.ExecContext(ctx, "INSERT INTO radio_log (artist, title, played_datetime) VALUES (?, ?, ?)", artist, title, dateStr)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (m *DBManager) WasSongInLastNPlays(ctx context.Context, artist, title string, n int) (bool, error) {
	// OFFSET 1 to exclude the current song which was just logged
	rows, err := m.db.QueryContext(ctx, "SELECT artist, title FROM radio_log ORDER BY id DESC LIMIT ? OFFSET 1", n)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var rowArtist, rowTitle string
		if err := rows.Scan(&rowArtist, &rowTitle); err != nil {
			return false, err
		}
		if rowArtist == artist && rowTitle == title {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

type RadioLog struct {
	ID             int
	Artist         string
	Title          string
	PlayedDatetime string
}

func (m *DBManager) GetRadioLogs(ctx context.Context, limit int) ([]RadioLog, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT id, artist, title, played_datetime FROM radio_log ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var logs []RadioLog
	for rows.Next() {
		var l RadioLog
		if err := rows.Scan(&l.ID, &l.Artist, &l.Title, &l.PlayedDatetime); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return logs, nil
}

type AlertRecord struct {
	ID        int
	Level     string
	Title     string
	Message   string
	CreatedAt string
}

func (m *DBManager) SaveAlert(ctx context.Context, level, title, message string, createdAt time.Time) error {
	_, err := m.db.ExecContext(ctx, "INSERT INTO alerts (level, title, message, created_at) VALUES (?, ?, ?, ?)", level, title, message, createdAt.UTC().Format(time.RFC3339))
	return err
}

func (m *DBManager) GetRecentAlerts(ctx context.Context, limit int) ([]AlertRecord, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT id, level, title, message, created_at FROM alerts ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var alerts []AlertRecord
	for rows.Next() {
		var a AlertRecord
		if err := rows.Scan(&a.ID, &a.Level, &a.Title, &a.Message, &a.CreatedAt); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return alerts, nil
}

func normalizeCampaignArtistKey(artist string) string {
	return strings.ToLower(strings.TrimSpace(artist))
}

func (m *DBManager) RegisterUsedAudioHash(ctx context.Context, contentHash string, firstSeenAt time.Time) error {
	if strings.TrimSpace(contentHash) == "" {
		return fmt.Errorf("content hash cannot be empty")
	}

	_, err := m.db.ExecContext(
		ctx,
		"INSERT OR IGNORE INTO used_audio_hashes (content_hash, first_seen_at) VALUES (?, ?)",
		contentHash,
		firstSeenAt.Format("2006-01-02 15:04:05"),
	)
	return err
}

func (m *DBManager) IsAudioHashUsed(ctx context.Context, contentHash string) (bool, error) {
	var exists int
	err := m.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM used_audio_hashes WHERE content_hash = ?)", contentHash).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}

func (m *DBManager) GetLatestRadioLogID(ctx context.Context) (int64, error) {
	var latestID sql.NullInt64
	err := m.db.QueryRowContext(ctx, "SELECT MAX(id) FROM radio_log").Scan(&latestID)
	if err != nil {
		return 0, err
	}
	if !latestID.Valid {
		return 0, nil
	}
	return latestID.Int64, nil
}

func (m *DBManager) CanSendCampaignArtist(ctx context.Context, campaignArtist string, upperExclusiveRadioLogID int64) (bool, error) {
	var lastSentRadioLogID int64
	err := m.db.QueryRowContext(
		ctx,
		"SELECT last_sent_radio_log_id FROM campaign_send_state WHERE campaign_artist = ?",
		normalizeCampaignArtistKey(campaignArtist),
	).Scan(&lastSentRadioLogID)
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}

	if upperExclusiveRadioLogID <= lastSentRadioLogID {
		return false, nil
	}

	var exists int
	err = m.db.QueryRowContext(
		ctx,
		`SELECT EXISTS(
			SELECT 1
			FROM radio_log
			WHERE id > ?
			  AND id < ?
			  AND LOWER(artist) NOT LIKE '%' || ? || '%'
		)`,
		lastSentRadioLogID,
		upperExclusiveRadioLogID,
		normalizeCampaignArtistKey(campaignArtist),
	).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists == 1, nil
}

func (m *DBManager) RecordSuccessfulSend(ctx context.Context, campaignArtist, artist, title, contentHash string, radioLogID int64, date time.Time) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	playDateStr := date.Format("2006-01-02 15:04")
	sentAtStr := date.Format("2006-01-02 15:04:05")

	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO played_songs (artist, title, played_datetime) VALUES (?, ?, ?)", artist, title, playDateStr); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO used_audio_hashes (content_hash, first_seen_at) VALUES (?, ?)", contentHash, sentAtStr); err != nil {
		return err
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO campaign_send_state (campaign_artist, last_sent_radio_log_id, last_sent_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(campaign_artist) DO UPDATE
		 SET last_sent_radio_log_id = excluded.last_sent_radio_log_id,
		     last_sent_at = excluded.last_sent_at`,
		normalizeCampaignArtistKey(campaignArtist),
		radioLogID,
		sentAtStr,
	); err != nil {
		return err
	}

	return tx.Commit()
}

// RecordSongPlay records that a song was played today.
func (m *DBManager) RecordSongPlay(ctx context.Context, artist, title string, date time.Time) error {
	dateStr := date.Format("2006-01-02 15:04")
	_, err := m.db.ExecContext(ctx, "INSERT OR IGNORE INTO played_songs (artist, title, played_datetime) VALUES (?, ?, ?)", artist, title, dateStr)
	return err
}

// SetKillSwitch updates the kill switch state in the database
func (m *DBManager) SetKillSwitch(ctx context.Context, active bool) error {
	return m.setBoolSetting(ctx, "kill_switch", active)
}

// IsKillSwitchActive reads the kill switch state from the database
func (m *DBManager) IsKillSwitchActive(ctx context.Context) (bool, error) {
	return m.getBoolSetting(ctx, "kill_switch", false)
}

func (m *DBManager) SetGatheringSignatures(ctx context.Context, active bool) error {
	return m.setBoolSetting(ctx, "gathering_signatures", active)
}

func (m *DBManager) IsGatheringSignaturesEnabled(ctx context.Context) (bool, error) {
	return m.getBoolSetting(ctx, "gathering_signatures", true)
}

func (m *DBManager) setBoolSetting(ctx context.Context, key string, active bool) error {
	valStr := "false"
	if active {
		valStr = "true"
	}
	_, err := m.db.ExecContext(ctx, "INSERT OR REPLACE INTO app_settings (key, value) VALUES (?, ?)", key, valStr)
	return err
}

func (m *DBManager) getBoolSetting(ctx context.Context, key string, defaultValue bool) (bool, error) {
	var valStr string
	err := m.db.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE key = ?", key).Scan(&valStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return defaultValue, nil
		}
		return defaultValue, err
	}
	return valStr == "true", nil
}

func (m *DBManager) GetDailySchedule(ctx context.Context, date string) (string, error) {
	var targetMatches string
	err := m.db.QueryRowContext(ctx, "SELECT target_matches FROM daily_schedule WHERE date = ?", date).Scan(&targetMatches)
	if err != nil {
		return "", err
	}
	return targetMatches, nil
}

func (m *DBManager) CreateDailyScheduleIfAbsent(ctx context.Context, date, targetMatches string) error {
	_, err := m.db.ExecContext(ctx, "INSERT OR IGNORE INTO daily_schedule (date, target_matches) VALUES (?, ?)", date, targetMatches)
	return err
}

func (m *DBManager) SetDailySchedule(ctx context.Context, date, targetMatches string) error {
	_, err := m.db.ExecContext(ctx, "INSERT INTO daily_schedule (date, target_matches) VALUES (?, ?) ON CONFLICT(date) DO UPDATE SET target_matches = excluded.target_matches", date, targetMatches)
	return err
}

func (m *DBManager) GetAllSchedules(ctx context.Context) (map[string]string, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT date, target_matches FROM daily_schedule ORDER BY date ASC")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	schedules := make(map[string]string)
	for rows.Next() {
		var date string
		var targetMatches string
		if err := rows.Scan(&date, &targetMatches); err != nil {
			return nil, err
		}
		schedules[date] = targetMatches
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return schedules, nil
}

type SignatureFile struct {
	Bucket         string
	Filename       string
	RecordedAt     time.Time
	CampaignArtist string
	Transcript     string
	Tags           string
}

func (m *DBManager) UpsertSignatureFile(ctx context.Context, bucket, filename string, recordedAt time.Time, campaignArtist, transcript, tags string) error {
	_, err := m.db.ExecContext(
		ctx,
		`INSERT INTO signature_files (bucket, filename, recorded_at, campaign_artist, transcript, tags)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(bucket, filename) DO UPDATE SET
		   recorded_at = excluded.recorded_at,
		   campaign_artist = excluded.campaign_artist,
		   transcript = excluded.transcript,
		   tags = excluded.tags`,
		bucket,
		filename,
		recordedAt.Format(time.RFC3339),
		campaignArtist,
		transcript,
		tags,
	)
	return err
}

func (m *DBManager) UpdateSignatureTranscript(ctx context.Context, bucket, filename, transcript string) error {
	_, err := m.db.ExecContext(
		ctx,
		`UPDATE signature_files SET transcript = ? WHERE bucket = ? AND filename = ?`,
		transcript,
		bucket,
		filename,
	)
	return err
}

func (m *DBManager) GetSignatureFile(ctx context.Context, bucket, filename string) (SignatureFile, error) {
	var meta SignatureFile
	var recordedAt string
	err := m.db.QueryRowContext(
		ctx,
		`SELECT bucket, filename, recorded_at, campaign_artist, transcript, tags
		 FROM signature_files
		 WHERE bucket = ? AND filename = ?`,
		bucket,
		filename,
	).Scan(&meta.Bucket, &meta.Filename, &recordedAt, &meta.CampaignArtist, &meta.Transcript, &meta.Tags)
	if err != nil {
		return SignatureFile{}, err
	}
	parsed, err := time.Parse(time.RFC3339, recordedAt)
	if err != nil {
		return SignatureFile{}, err
	}
	meta.RecordedAt = parsed
	return meta, nil
}

func (m *DBManager) CopySignatureFile(ctx context.Context, fromBucket, toBucket, filename string) error {
	meta, err := m.GetSignatureFile(ctx, fromBucket, filename)
	if err != nil {
		return err
	}
	return m.UpsertSignatureFile(ctx, toBucket, filename, meta.RecordedAt, meta.CampaignArtist, meta.Transcript, meta.Tags)
}

func (m *DBManager) DeleteSignatureFile(ctx context.Context, bucket, filename string) error {
	_, err := m.db.ExecContext(ctx, "DELETE FROM signature_files WHERE bucket = ? AND filename = ?", bucket, filename)
	return err
}

func (m *DBManager) AddCampaignPhrase(ctx context.Context, campaignArtist, phrase string) error {
	artist := normalizeCampaignArtistKey(campaignArtist)
	_, err := m.db.ExecContext(ctx, "INSERT OR IGNORE INTO campaign_phrases (campaign_artist, phrase) VALUES (?, ?)", artist, strings.ToLower(strings.TrimSpace(phrase)))
	if err == nil {
		m.invalidateCampaignPhraseCache(artist)
	}
	return err
}

func (m *DBManager) GetCampaignPhrases(ctx context.Context, campaignArtist string) ([]string, error) {
	artist := normalizeCampaignArtistKey(campaignArtist)
	m.phraseMu.RLock()
	cached, ok := m.phraseCache[artist]
	m.phraseMu.RUnlock()
	if ok {
		return cached, nil
	}

	m.phraseMu.Lock()
	defer m.phraseMu.Unlock()
	if phrases, ok := m.phraseCache[artist]; ok {
		return phrases, nil
	}
	rows, err := m.db.QueryContext(ctx, "SELECT phrase FROM campaign_phrases WHERE campaign_artist = ?", artist)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var phrases []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		phrases = append(phrases, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if m.phraseCache == nil {
		m.phraseCache = make(map[string][]string)
	}
	m.phraseCache[artist] = phrases
	return phrases, nil
}

type CampaignPhrase struct {
	CampaignArtist string `json:"campaign_artist"`
	Phrase         string `json:"phrase"`
}

func (m *DBManager) ListAllCampaignPhrases(ctx context.Context) (map[string][]CampaignPhrase, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT campaign_artist, phrase FROM campaign_phrases ORDER BY campaign_artist, phrase")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	phrases := make(map[string][]CampaignPhrase)
	for rows.Next() {
		var p CampaignPhrase
		if err := rows.Scan(&p.CampaignArtist, &p.Phrase); err != nil {
			return nil, err
		}
		phrases[p.CampaignArtist] = append(phrases[p.CampaignArtist], p)
	}
	return phrases, rows.Err()
}

func (m *DBManager) DeleteCampaignPhrase(ctx context.Context, campaignArtist, phrase string) error {
	artist := normalizeCampaignArtistKey(campaignArtist)
	_, err := m.db.ExecContext(ctx, "DELETE FROM campaign_phrases WHERE campaign_artist = ? AND phrase = ?", artist, strings.ToLower(strings.TrimSpace(phrase)))
	if err == nil {
		m.invalidateCampaignPhraseCache(artist)
	}
	return err
}

func (m *DBManager) invalidateCampaignPhraseCache(artist string) {
	m.phraseMu.Lock()
	delete(m.phraseCache, artist)
	m.phraseMu.Unlock()
}
