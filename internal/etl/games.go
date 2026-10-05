// Package etl extracts bot state and logs, strips member identifiers, and
// shapes them for the store.
package etl

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/blindfaithwargaming/bfwg-metrics/internal/store"
)

// rawGame declares only the fields we keep. Member identifiers, display names,
// thread titles and free-text notes in the source file are never decoded.
type rawGame struct {
	ID            string      `json:"id"`
	ClubNightDate string      `json:"club_night_date"`
	ReportedAt    string      `json:"reported_at"`
	Game          string      `json:"game"`
	Result        string      `json:"result"`
	PointsSize    *int        `json:"points_size"`
	Players       []rawPlayer `json:"players"`
}

type rawPlayer struct {
	Faction string `json:"faction"`
	Side    string `json:"side"`
	Result  string `json:"result"`
}

type gamesFile struct {
	Games []rawGame `json:"games"`
}

var (
	validResults  = map[string]bool{"side1": true, "side2": true, "draw": true}
	validSides    = map[string]bool{"side1": true, "side2": true}
	validOutcomes = map[string]bool{"win": true, "loss": true, "draw": true}
)

// ParseGames reads the bot's club_night_games.json. Malformed games are
// skipped rather than failing the whole load, mirroring how the bot itself
// tolerates partially bad state.
func ParseGames(r io.Reader) (games []store.Game, read int, err error) {
	var file gamesFile
	if err := json.NewDecoder(r).Decode(&file); err != nil {
		return nil, 0, fmt.Errorf("decode games file: %w", err)
	}
	for _, rg := range file.Games {
		g, ok := transformGame(rg)
		if ok {
			games = append(games, g)
		}
	}
	return games, len(file.Games), nil
}

func transformGame(rg rawGame) (store.Game, bool) {
	if rg.ID == "" || !validResults[rg.Result] {
		return store.Game{}, false
	}
	if _, err := time.Parse(time.DateOnly, rg.ClubNightDate); err != nil {
		return store.Game{}, false
	}
	reportedAt, err := time.Parse(time.RFC3339Nano, rg.ReportedAt)
	if err != nil {
		return store.Game{}, false
	}
	game := strings.ToLower(strings.TrimSpace(rg.Game))
	if game == "" {
		// Games reported before multi-game support carry no game key.
		game = "heresy"
	}
	g := store.Game{
		ID:            rg.ID,
		ClubNightDate: rg.ClubNightDate,
		ReportedAt:    reportedAt,
		Game:          game,
		Result:        rg.Result,
		PointsSize:    rg.PointsSize,
	}
	for _, p := range rg.Players {
		faction := strings.TrimSpace(p.Faction)
		if faction == "" || !validSides[p.Side] || !validOutcomes[p.Result] {
			continue
		}
		g.Factions = append(g.Factions, store.GameFaction{
			Side: p.Side, Faction: faction, Outcome: p.Result,
		})
	}
	return g, true
}
