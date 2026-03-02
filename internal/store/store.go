package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// ── GORM models (map to existing tables) ────────────────────────────

type windowModel struct {
	ID           int64   `gorm:"column:id;primaryKey"`
	SessionID    string  `gorm:"column:session_id"`
	StartTime    string  `gorm:"column:start_time"`
	BTCOpen      float64 `gorm:"column:btc_open"`
	BTCClose     float64 `gorm:"column:btc_close"`
	Change       float64 `gorm:"column:change"`
	Direction    string  `gorm:"column:direction"`
	MarketSignal string  `gorm:"column:market_signal"`
	SampleCount  int     `gorm:"column:sample_count"`
	CreatedAt    string  `gorm:"column:created_at"`
}

func (windowModel) TableName() string { return "windows" }

type sampleModel struct {
	ID          int64   `gorm:"column:id;primaryKey"`
	WindowID    int64   `gorm:"column:window_id"`
	ElapsedMs   int64   `gorm:"column:elapsed_ms"`
	RemainingMs int64   `gorm:"column:remaining_ms"`
	BTCPrice    float64 `gorm:"column:btc_price"`
	YesAsk      float64 `gorm:"column:yes_ask"`
	YesBid      float64 `gorm:"column:yes_bid"`
	NoAsk       float64 `gorm:"column:no_ask"`
	NoBid       float64 `gorm:"column:no_bid"`
}

func (sampleModel) TableName() string { return "samples" }

type redeemModel struct {
	ConditionID string `gorm:"column:condition_id;primaryKey"`
	TxID        string `gorm:"column:tx_id"`
	EventSlug   string `gorm:"column:event_slug"`
	CreatedAt   string `gorm:"column:created_at"`
}

func (redeemModel) TableName() string { return "redeems" }

type paperTradeModel struct {
	ID          int64   `gorm:"column:id;primaryKey"`
	SessionID   string  `gorm:"column:session_id"`
	SlotLabel   string  `gorm:"column:slot_label"`
	EntryTime   string  `gorm:"column:entry_time"`
	WindowStart string  `gorm:"column:window_start"`
	Side        string  `gorm:"column:side"`
	BuyPrice    float64 `gorm:"column:buy_price"`
	Size        float64 `gorm:"column:size"`
	RemainingNs int64   `gorm:"column:remaining_ns"`
	Change5m    float64 `gorm:"column:change5m"`
	Resolved    int     `gorm:"column:resolved"`
	Won         int     `gorm:"column:won"`
	PnL         float64 `gorm:"column:pnl"`
	FinalDir    string  `gorm:"column:final_dir"`
	Live        int     `gorm:"column:live"`
}

func (paperTradeModel) TableName() string { return "paper_trades" }

// ── Public types (unchanged) ────────────────────────────────────────

// Store provides SQLite persistence for backtest data.
type Store struct {
	db        *gorm.DB
	sessionID string
}

// WindowRow represents a completed 5-minute window stored in the database.
type WindowRow struct {
	ID           int64
	SessionID    string
	StartTime    time.Time
	BTCOpen      float64
	BTCClose     float64
	Change       float64
	Direction    string
	MarketSignal string // "Up"/"Down" if ask >= 0.99 detected, auxiliary only
	SampleCount  int
	CreatedAt    time.Time
}

// SampleRow represents a market snapshot within a window.
type SampleRow struct {
	ElapsedMs   int64
	RemainingMs int64
	BTCPrice    float64
	YesAsk      float64
	YesBid      float64
	NoAsk       float64
	NoBid       float64
}

// WindowWithSamples pairs a window with all its sample data.
type WindowWithSamples struct {
	Window  WindowRow
	Samples []SampleRow
}

// PaperTradeRow represents a paper trade stored in the database.
type PaperTradeRow struct {
	SlotLabel   string
	EntryTime   time.Time
	WindowStart time.Time
	Side        string
	BuyPrice    float64
	Size        float64
	RemainingNs int64
	Change5m    float64
	Resolved    bool
	Won         bool
	PnL         float64
	FinalDir    string
	Live        bool
}

// PaperSessionSummary holds aggregate stats for a single paper trading session.
type PaperSessionSummary struct {
	SessionID string
	StartedAt time.Time
	EndedAt   time.Time
	Trades    int
	Wins      int
	Losses    int
	TotalPnL  float64
}

// ── Constructor ─────────────────────────────────────────────────────

// New opens (or creates) a SQLite database and initializes the schema.
// sessionID groups windows from the same engine run.
func New(dbPath, sessionID string) (*Store, error) {
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Limit to a single connection to avoid SQLITE_BUSY on concurrent writes.
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := createTables(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}

	return &Store{db: db, sessionID: sessionID}, nil
}

func createTables(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS windows (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id   TEXT    NOT NULL,
			start_time   TEXT    NOT NULL,
			btc_open     REAL   NOT NULL,
			btc_close    REAL   NOT NULL,
			change       REAL   NOT NULL,
			direction    TEXT   NOT NULL,
			sample_count INTEGER NOT NULL,
			created_at   TEXT   NOT NULL DEFAULT (datetime('now'))
		);

		CREATE TABLE IF NOT EXISTS samples (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			window_id    INTEGER NOT NULL REFERENCES windows(id),
			elapsed_ms   INTEGER NOT NULL,
			remaining_ms INTEGER NOT NULL,
			btc_price    REAL    NOT NULL,
			yes_ask      REAL    NOT NULL,
			yes_bid      REAL    NOT NULL,
			no_ask       REAL    NOT NULL,
			no_bid       REAL    NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_windows_session ON windows(session_id);
		CREATE INDEX IF NOT EXISTS idx_windows_start   ON windows(start_time);
		CREATE INDEX IF NOT EXISTS idx_samples_window   ON samples(window_id);

		CREATE TABLE IF NOT EXISTS redeems (
			condition_id TEXT PRIMARY KEY,
			tx_id        TEXT NOT NULL DEFAULT '',
			event_slug   TEXT NOT NULL DEFAULT '',
			created_at   TEXT NOT NULL DEFAULT (datetime('now'))
		);

		CREATE TABLE IF NOT EXISTS paper_trades (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			slot_label   TEXT    NOT NULL,
			entry_time   TEXT    NOT NULL,
			window_start TEXT    NOT NULL,
			side         TEXT    NOT NULL,
			buy_price    REAL    NOT NULL,
			size         REAL    NOT NULL,
			remaining_ns INTEGER NOT NULL,
			change5m     REAL    NOT NULL,
			resolved     INTEGER NOT NULL DEFAULT 0,
			won          INTEGER NOT NULL DEFAULT 0,
			pnl          REAL    NOT NULL DEFAULT 0,
			final_dir    TEXT    NOT NULL DEFAULT ''
		);

		CREATE INDEX IF NOT EXISTS idx_paper_trades_slot   ON paper_trades(slot_label);
		CREATE INDEX IF NOT EXISTS idx_paper_trades_window ON paper_trades(window_start);
	`)
	if err != nil {
		return err
	}

	// Idempotent migration: add session_id column to paper_trades.
	db.Exec(`ALTER TABLE paper_trades ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`)
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_paper_trades_session ON paper_trades(session_id)`)
	if err != nil {
		return err
	}

	// Idempotent migration: add live column to paper_trades.
	db.Exec(`ALTER TABLE paper_trades ADD COLUMN live INTEGER NOT NULL DEFAULT 0`)

	// Idempotent migration: add market_signal column to windows.
	db.Exec(`ALTER TABLE windows ADD COLUMN market_signal TEXT NOT NULL DEFAULT ''`)
	return nil
}

// ── Window operations ───────────────────────────────────────────────

// InsertWindow stores a completed window and all its samples in a single transaction.
// marketSignal is an auxiliary indicator ("Up"/"Down") from ask >= 0.99 detection.
func (s *Store) InsertWindow(startTime time.Time, btcOpen, btcClose, change float64,
	direction, marketSignal string, samples []SampleRow) error {

	return s.db.Transaction(func(tx *gorm.DB) error {
		w := windowModel{
			SessionID:    s.sessionID,
			StartTime:    startTime.Format(time.RFC3339),
			BTCOpen:      btcOpen,
			BTCClose:     btcClose,
			Change:       change,
			Direction:    direction,
			MarketSignal: marketSignal,
			SampleCount:  len(samples),
			CreatedAt:    time.Now().UTC().Format("2006-01-02 15:04:05"),
		}
		if err := tx.Create(&w).Error; err != nil {
			return fmt.Errorf("insert window: %w", err)
		}

		if len(samples) > 0 {
			models := make([]sampleModel, len(samples))
			for i, sa := range samples {
				models[i] = sampleModel{
					WindowID:    w.ID,
					ElapsedMs:   sa.ElapsedMs,
					RemainingMs: sa.RemainingMs,
					BTCPrice:    sa.BTCPrice,
					YesAsk:      sa.YesAsk,
					YesBid:      sa.YesBid,
					NoAsk:       sa.NoAsk,
					NoBid:       sa.NoBid,
				}
			}
			if err := tx.CreateInBatches(models, 100).Error; err != nil {
				return fmt.Errorf("insert samples: %w", err)
			}
		}
		return nil
	})
}

// QuerySessionWindows returns all completed windows for a given session, ordered by start_time.
func (s *Store) QuerySessionWindows(sessionID string) ([]WindowRow, error) {
	var models []windowModel
	if err := s.db.Where("session_id = ?", sessionID).Order("start_time ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("query session windows: %w", err)
	}

	result := make([]WindowRow, len(models))
	for i, m := range models {
		result[i] = windowModelToRow(m)
	}
	return result, nil
}

// QueryAllWindows returns all completed windows with their samples, ordered by start_time.
// Uses batch query (2 SQL statements) instead of N+1 queries.
func (s *Store) QueryAllWindows() ([]WindowWithSamples, error) {
	var models []windowModel
	if err := s.db.Order("start_time ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("query windows: %w", err)
	}
	return s.batchLoadSamples(models)
}

// QueryWindowsByTimeRange returns windows within [from, to] with their samples.
// Zero-value from/to means unbounded on that side; both zero = all windows.
// Uses batch query (2 SQL statements) instead of N+1 queries.
func (s *Store) QueryWindowsByTimeRange(from, to time.Time) ([]WindowWithSamples, error) {
	query := s.db.Order("start_time ASC")
	if !from.IsZero() {
		query = query.Where("start_time >= ?", from.Format(time.RFC3339))
	}
	if !to.IsZero() {
		query = query.Where("start_time <= ?", to.Format(time.RFC3339))
	}

	var models []windowModel
	if err := query.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("query windows by time range: %w", err)
	}
	return s.batchLoadSamples(models)
}

// QueryWindowsAfter returns windows with start_time strictly after the given time.
// Used for incremental loading of new windows appended after the last known time.
func (s *Store) QueryWindowsAfter(after time.Time) ([]WindowWithSamples, error) {
	var models []windowModel
	if err := s.db.Where("start_time > ?", after.Format(time.RFC3339)).
		Order("start_time ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("query windows after: %w", err)
	}
	return s.batchLoadSamples(models)
}

// batchLoadSamples loads all samples for the given window models in a single batch query.
func (s *Store) batchLoadSamples(models []windowModel) ([]WindowWithSamples, error) {
	if len(models) == 0 {
		return nil, nil
	}

	ids := make([]int64, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}

	// Query samples in batches to avoid SQLite IN clause limits.
	const batchSize = 500
	var allSamples []sampleModel
	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		var batch []sampleModel
		if err := s.db.Where("window_id IN ?", ids[i:end]).Order("window_id ASC, elapsed_ms ASC").Find(&batch).Error; err != nil {
			return nil, fmt.Errorf("batch query samples: %w", err)
		}
		allSamples = append(allSamples, batch...)
	}

	sampleMap := make(map[int64][]SampleRow, len(models))
	for _, m := range allSamples {
		sampleMap[m.WindowID] = append(sampleMap[m.WindowID], SampleRow{
			ElapsedMs:   m.ElapsedMs,
			RemainingMs: m.RemainingMs,
			BTCPrice:    m.BTCPrice,
			YesAsk:      m.YesAsk,
			YesBid:      m.YesBid,
			NoAsk:       m.NoAsk,
			NoBid:       m.NoBid,
		})
	}

	result := make([]WindowWithSamples, len(models))
	for i, m := range models {
		result[i] = WindowWithSamples{
			Window:  windowModelToRow(m),
			Samples: sampleMap[m.ID],
		}
	}
	return result, nil
}

// LatestWindow returns the most recent completed window across all sessions.
// Returns nil if no windows exist.
func (s *Store) LatestWindow() (*WindowRow, error) {
	var m windowModel
	err := s.db.Order("start_time DESC").First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("query latest window: %w", err)
	}
	row := windowModelToRow(m)
	return &row, nil
}

// RecentWindows returns the N most recent completed windows (newest first).
func (s *Store) RecentWindows(n int) ([]WindowRow, error) {
	var models []windowModel
	if err := s.db.Order("start_time DESC").Limit(n).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("query recent windows: %w", err)
	}
	result := make([]WindowRow, len(models))
	for i, m := range models {
		result[i] = windowModelToRow(m)
	}
	return result, nil
}

// CorrectWindowDirection updates the direction of a stored window when the
// official market_resolved result differs from the local candle-based direction.
func (s *Store) CorrectWindowDirection(windowStart time.Time, direction string) error {
	return s.db.Model(&windowModel{}).
		Where("start_time = ? AND session_id = ?", windowStart.Format(time.RFC3339), s.sessionID).
		Update("direction", direction).Error
}

func (s *Store) querySamples(windowID int64) ([]SampleRow, error) {
	var models []sampleModel
	if err := s.db.Where("window_id = ?", windowID).Order("elapsed_ms ASC").Find(&models).Error; err != nil {
		return nil, err
	}

	samples := make([]SampleRow, len(models))
	for i, m := range models {
		samples[i] = SampleRow{
			ElapsedMs:   m.ElapsedMs,
			RemainingMs: m.RemainingMs,
			BTCPrice:    m.BTCPrice,
			YesAsk:      m.YesAsk,
			YesBid:      m.YesBid,
			NoAsk:       m.NoAsk,
			NoBid:       m.NoBid,
		}
	}
	return samples, nil
}

func windowModelToRow(m windowModel) WindowRow {
	startTime, _ := time.Parse(time.RFC3339, m.StartTime)
	createdAt, _ := time.Parse("2006-01-02 15:04:05", m.CreatedAt)
	return WindowRow{
		ID:           m.ID,
		SessionID:    m.SessionID,
		StartTime:    startTime,
		BTCOpen:      m.BTCOpen,
		BTCClose:     m.BTCClose,
		Change:       m.Change,
		Direction:    m.Direction,
		MarketSignal: m.MarketSignal,
		SampleCount:  m.SampleCount,
		CreatedAt:    createdAt,
	}
}

// ── Redeem operations ───────────────────────────────────────────────

// IsRedeemed reports whether a conditionID has already been redeemed.
func (s *Store) IsRedeemed(conditionID string) bool {
	var count int64
	s.db.Model(&redeemModel{}).Where("condition_id = ?", conditionID).Count(&count)
	return count > 0
}

// InsertRedeem records a successful redeem for a conditionID.
func (s *Store) InsertRedeem(conditionID, txID, eventSlug string) error {
	m := redeemModel{
		ConditionID: conditionID,
		TxID:        txID,
		EventSlug:   eventSlug,
		CreatedAt:   time.Now().UTC().Format("2006-01-02 15:04:05"),
	}
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&m).Error
}

// DeleteRedeem removes a redeem record, allowing the condition to be retried.
func (s *Store) DeleteRedeem(conditionID string) error {
	return s.db.Where("condition_id = ?", conditionID).Delete(&redeemModel{}).Error
}

// ── Paper trade operations ──────────────────────────────────────────

// UnresolvedTradesForWindow returns all unresolved paper trades for a given
// window start, across ALL sessions. Used during fast restart to detect if a
// slot already placed an order in the current 5m window before the crash.
func (s *Store) UnresolvedTradesForWindow(windowStart time.Time) ([]PaperTradeRow, error) {
	var models []paperTradeModel
	if err := s.db.Where("window_start = ? AND resolved = 0",
		windowStart.Format(time.RFC3339)).
		Order("entry_time ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("query unresolved trades: %w", err)
	}

	result := make([]PaperTradeRow, len(models))
	for i, m := range models {
		entryTime, _ := time.Parse(time.RFC3339Nano, m.EntryTime)
		ws, _ := time.Parse(time.RFC3339, m.WindowStart)
		result[i] = PaperTradeRow{
			SlotLabel:   m.SlotLabel,
			EntryTime:   entryTime,
			WindowStart: ws,
			Side:        m.Side,
			BuyPrice:    m.BuyPrice,
			Size:        m.Size,
			RemainingNs: m.RemainingNs,
			Change5m:    m.Change5m,
			Live:        m.Live != 0,
		}
	}
	return result, nil
}

// InsertPaperTrade persists a new paper trade (unresolved).
func (s *Store) InsertPaperTrade(row PaperTradeRow) error {
	liveInt := 0
	if row.Live {
		liveInt = 1
	}
	m := paperTradeModel{
		SessionID:   s.sessionID,
		SlotLabel:   row.SlotLabel,
		EntryTime:   row.EntryTime.Format(time.RFC3339Nano),
		WindowStart: row.WindowStart.Format(time.RFC3339),
		Side:        row.Side,
		BuyPrice:    row.BuyPrice,
		Size:        row.Size,
		RemainingNs: row.RemainingNs,
		Change5m:    row.Change5m,
		Live:        liveInt,
	}
	return s.db.Create(&m).Error
}

// ResolvePaperTrades marks all unresolved paper trades for a given window as resolved.
// Only resolves trades belonging to the current session.
// PnL is computed in SQL: win → (1-buy_price)*size, loss → -buy_price*size.
func (s *Store) ResolvePaperTrades(windowStart time.Time, direction string) error {
	return s.db.Exec(`
		UPDATE paper_trades SET
			resolved  = 1,
			final_dir = ?,
			won       = CASE WHEN side = ? THEN 1 ELSE 0 END,
			pnl       = CASE WHEN side = ? THEN (1.0 - buy_price) * size ELSE -buy_price * size END
		WHERE window_start = ? AND resolved = 0 AND session_id = ?`,
		direction, direction, direction,
		windowStart.Format(time.RFC3339),
		s.sessionID,
	).Error
}

// CorrectPaperTrades re-resolves already-resolved paper trades for a window
// when the official market_resolved direction differs from the local candle.
// Unlike ResolvePaperTrades (which targets resolved=0), this updates resolved=1 rows.
func (s *Store) CorrectPaperTrades(windowStart time.Time, direction string) error {
	return s.db.Exec(`
		UPDATE paper_trades SET
			final_dir = ?,
			won       = CASE WHEN side = ? THEN 1 ELSE 0 END,
			pnl       = CASE WHEN side = ? THEN (1.0 - buy_price) * size ELSE -buy_price * size END
		WHERE window_start = ? AND resolved = 1 AND session_id = ?`,
		direction, direction, direction,
		windowStart.Format(time.RFC3339),
		s.sessionID,
	).Error
}

// PaperSessionSummaries returns aggregated summaries of historical paper sessions.
// excludeSession is excluded from results (pass current session ID).
// limit controls max results (0 = unlimited).
func (s *Store) PaperSessionSummaries(excludeSession string, limit int) ([]PaperSessionSummary, error) {
	query := `
		SELECT session_id,
		       MIN(entry_time), MAX(entry_time),
		       COUNT(*),
		       SUM(CASE WHEN resolved = 1 AND won = 1 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN resolved = 1 AND won = 0 THEN 1 ELSE 0 END),
		       SUM(pnl)
		FROM paper_trades
		WHERE session_id != ? AND session_id != ''
		GROUP BY session_id
		ORDER BY MIN(entry_time) DESC`

	args := []any{excludeSession}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Raw(query, args...).Rows()
	if err != nil {
		return nil, fmt.Errorf("query paper session summaries: %w", err)
	}
	defer rows.Close()

	var result []PaperSessionSummary
	for rows.Next() {
		var ss PaperSessionSummary
		var startStr, endStr string
		if err := rows.Scan(&ss.SessionID, &startStr, &endStr,
			&ss.Trades, &ss.Wins, &ss.Losses, &ss.TotalPnL); err != nil {
			return nil, fmt.Errorf("scan paper session summary: %w", err)
		}
		ss.StartedAt, _ = time.Parse(time.RFC3339Nano, startStr)
		ss.EndedAt, _ = time.Parse(time.RFC3339Nano, endStr)
		result = append(result, ss)
	}
	return result, rows.Err()
}

// PurgePaperSessions deletes paper trades from sessions beyond the keep limit.
// Sessions are ranked by earliest trade time (newest first); only the top keep are retained.
func (s *Store) PurgePaperSessions(keep int) error {
	if keep <= 0 {
		return nil
	}
	return s.db.Exec(`
		DELETE FROM paper_trades
		WHERE session_id != '' AND session_id NOT IN (
			SELECT session_id FROM (
				SELECT session_id, MIN(entry_time) AS first_trade
				FROM paper_trades
				WHERE session_id != ''
				GROUP BY session_id
				ORDER BY first_trade DESC
				LIMIT ?
			)
		)`, keep).Error
}

// PaperSessionTrades returns all trades for a given session, ordered by entry time.
func (s *Store) PaperSessionTrades(sessionID string) ([]PaperTradeRow, error) {
	var models []paperTradeModel
	if err := s.db.Where("session_id = ?", sessionID).Order("entry_time ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("query paper session trades: %w", err)
	}

	result := make([]PaperTradeRow, len(models))
	for i, m := range models {
		entryTime, _ := time.Parse(time.RFC3339Nano, m.EntryTime)
		windowStart, _ := time.Parse(time.RFC3339, m.WindowStart)
		result[i] = PaperTradeRow{
			SlotLabel:   m.SlotLabel,
			EntryTime:   entryTime,
			WindowStart: windowStart,
			Side:        m.Side,
			BuyPrice:    m.BuyPrice,
			Size:        m.Size,
			RemainingNs: m.RemainingNs,
			Change5m:    m.Change5m,
			Resolved:    m.Resolved != 0,
			Won:         m.Won != 0,
			PnL:         m.PnL,
			FinalDir:    m.FinalDir,
			Live:        m.Live != 0,
		}
	}
	return result, nil
}

// ── Stats & lifecycle ───────────────────────────────────────────────

// SessionID returns the current session identifier.
func (s *Store) SessionID() string {
	return s.sessionID
}

// Stats returns basic statistics about the database.
func (s *Store) Stats() (sessions, windows, samples int, err error) {
	var sess int64
	if err = s.db.Raw(`SELECT COUNT(DISTINCT session_id) FROM windows`).Scan(&sess).Error; err != nil {
		return
	}
	sessions = int(sess)

	var win int64
	if err = s.db.Raw(`SELECT COUNT(*) FROM windows`).Scan(&win).Error; err != nil {
		return
	}
	windows = int(win)

	var samp int64
	if err = s.db.Raw(`SELECT COUNT(*) FROM samples`).Scan(&samp).Error; err != nil {
		return
	}
	samples = int(samp)
	return
}

// Close closes the database connection.
func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
