// Package gamify holds invite levels, points, badges and the ranking.
package gamify

import (
	"database/sql"
	"time"
)

const (
	PointsPerSignup       = 10
	PointsPerFirstMessage = 5
	RankingLimit          = 20
)

// Level is a rank tier unlocked by accepted invites.
type Level struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	MinAccepted int    `json:"min_accepted"`
	Quota       int    `json:"quota"`
}

// Levels are ordered by MinAccepted.
var Levels = []Level{
	{0, "Novato", 0, 2},
	{1, "Recrutador", 1, 3},
	{2, "Conector", 3, 5},
	{3, "Líder", 6, 8},
	{4, "Lenda", 12, 15},
}

// LevelFor returns the highest level reached with the given accepted invites.
func LevelFor(accepted int) Level {
	lv := Levels[0]
	for _, l := range Levels {
		if accepted >= l.MinAccepted {
			lv = l
		}
	}
	return lv
}

// NextLevel returns the following tier, or nil at the top.
func NextLevel(accepted int) *Level {
	cur := LevelFor(accepted)
	if cur.Index+1 >= len(Levels) {
		return nil
	}
	n := Levels[cur.Index+1]
	return &n
}

// Badge is a profile distinction.
type Badge struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// BadgesFor derives the earned badges.
func BadgesFor(accepted, points int, createdAt time.Time) []Badge {
	b := []Badge{}
	if accepted >= 1 {
		b = append(b, Badge{"first-invite", "Primeiro convite", "Trouxe a primeira pessoa"})
	}
	if accepted >= 3 {
		b = append(b, Badge{"connector", "Conector", "3 convites aceitos"})
	}
	if accepted >= 6 {
		b = append(b, Badge{"leader", "Líder", "6 convites aceitos"})
	}
	if accepted >= 12 {
		b = append(b, Badge{"legend", "Lenda", "12 convites aceitos"})
	}
	if points >= 50 {
		b = append(b, Badge{"active", "Em alta", "50 pontos ou mais"})
	}
	if !createdAt.IsZero() && time.Since(createdAt) > 30*24*time.Hour {
		b = append(b, Badge{"veteran", "Veterano", "Mais de 30 dias de casa"})
	}
	return b
}

// Stats is the caller's own progress.
type Stats struct {
	Accepted     int     `json:"accepted"`
	Pending      int     `json:"pending"`
	Quota        int     `json:"quota"`
	Points       int     `json:"points"`
	Level        Level   `json:"level"`
	Next         *Level  `json:"next_level,omitempty"`
	ToNext       int     `json:"to_next"`
	Progress     float64 `json:"progress"`
	Badges       []Badge `json:"badges"`
	Rank         int     `json:"rank"`
	TotalPlayers int     `json:"total_players"`
}

// AcceptedCount counts invites created by userID that someone used.
func AcceptedCount(db *sql.DB, userID string) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM invites WHERE created_by = ? AND used_by IS NOT NULL`, userID).Scan(&n)
	return n, err
}

// PendingCount counts unused, unexpired invites created by userID.
func PendingCount(db *sql.DB, userID string) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM invites WHERE created_by = ? AND used_by IS NULL AND expires_at > ?`,
		userID, time.Now()).Scan(&n)
	return n, err
}

// CanCreateInvite reports whether userID is under the pending-invite quota of their level.
func CanCreateInvite(db *sql.DB, userID string) (bool, int, error) {
	acc, err := AcceptedCount(db, userID)
	if err != nil {
		return false, 0, err
	}
	pend, err := PendingCount(db, userID)
	if err != nil {
		return false, 0, err
	}
	q := LevelFor(acc).Quota
	return pend < q, q, nil
}

// GetStats builds the caller's progress card.
func GetStats(db *sql.DB, userID string) (*Stats, error) {
	acc, err := AcceptedCount(db, userID)
	if err != nil {
		return nil, err
	}
	pend, err := PendingCount(db, userID)
	if err != nil {
		return nil, err
	}
	var points int
	var created time.Time
	if err := db.QueryRow(`SELECT points, created_at FROM users WHERE id = ?`, userID).Scan(&points, &created); err != nil {
		return nil, err
	}
	lv := LevelFor(acc)
	st := &Stats{Accepted: acc, Pending: pend, Quota: lv.Quota, Points: points, Level: lv,
		Badges: BadgesFor(acc, points, created), Progress: 1}
	if nx := NextLevel(acc); nx != nil {
		st.Next = nx
		st.ToNext = nx.MinAccepted - acc
		span := nx.MinAccepted - lv.MinAccepted
		st.Progress = float64(acc-lv.MinAccepted) / float64(span)
	}
	all, err := Ranking(db, 0)
	if err != nil {
		return nil, err
	}
	st.TotalPlayers = len(all)
	for _, e := range all {
		if e.UserID == userID {
			st.Rank = e.Rank
		}
	}
	return st, nil
}

// RankEntry is one line of the leaderboard.
type RankEntry struct {
	Rank     int    `json:"rank"`
	UserID   string `json:"user_id"`
	Login    string `json:"login"`
	Name     string `json:"name"`
	Points   int    `json:"points"`
	Accepted int    `json:"accepted"`
	Level    string `json:"level"`
}

// Ranking lists players by points, then accepted invites, then age. limit 0 means all.
func Ranking(db *sql.DB, limit int) ([]RankEntry, error) {
	q := `
		SELECT u.id, u.login, COALESCE(u.display_name, ''), u.points,
			(SELECT COUNT(*) FROM invites i WHERE i.created_by = u.id AND i.used_by IS NOT NULL) AS accepted
		FROM users u
		ORDER BY u.points DESC, accepted DESC, u.created_at ASC, u.login ASC`
	var args []any
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RankEntry{}
	for rows.Next() {
		var e RankEntry
		if err := rows.Scan(&e.UserID, &e.Login, &e.Name, &e.Points, &e.Accepted); err != nil {
			return nil, err
		}
		e.Rank = len(out) + 1
		e.Level = LevelFor(e.Accepted).Name
		if e.Name == "" {
			e.Name = e.Login
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MineInvite is one invite created by the caller.
type MineInvite struct {
	Code      string     `json:"code"`
	Status    string     `json:"status"` // active | used | expired
	ExpiresAt time.Time  `json:"expires_at"`
	UsedBy    string     `json:"used_by,omitempty"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
}

// ListMine returns the caller's invites, newest first.
func ListMine(db *sql.DB, userID string) ([]MineInvite, error) {
	rows, err := db.Query(`
		SELECT i.code, i.expires_at, COALESCE(u.login, ''), i.used_by IS NOT NULL, i.used_at
		FROM invites i
		LEFT JOIN users u ON u.id = i.used_by
		WHERE i.created_by = ?
		ORDER BY i.expires_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MineInvite{}
	now := time.Now()
	for rows.Next() {
		var m MineInvite
		var used bool
		var usedAt sql.NullTime
		if err := rows.Scan(&m.Code, &m.ExpiresAt, &m.UsedBy, &used, &usedAt); err != nil {
			return nil, err
		}
		if usedAt.Valid {
			t := usedAt.Time
			m.UsedAt = &t
		}
		switch {
		case used:
			m.Status = "used"
		case m.ExpiresAt.Before(now):
			m.Status = "expired"
		default:
			m.Status = "active"
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AwardFirstMessage gives the inviter points the first time an invited user posts.
func AwardFirstMessage(db *sql.DB, userID string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := tx.Exec(`UPDATE users SET first_msg_awarded = 1
		WHERE id = ? AND first_msg_awarded = 0 AND invited_by IS NOT NULL`, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		if _, err := tx.Exec(`UPDATE users SET points = points + ?
			WHERE id = (SELECT invited_by FROM users WHERE id = ?)`, PointsPerFirstMessage, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
