package etl

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"

	"github.com/blindfaithwargaming/bfwg-metrics/internal/store"
)

// logEntry is the subset of a Cloud Logging LogEntry, as emitted by
// `gcloud logging read --format=json`, that we need.
type logEntry struct {
	InsertID    string `json:"insertId"`
	Timestamp   string `json:"timestamp"`
	JSONPayload struct {
		Message string `json:"message"`
	} `json:"jsonPayload"`
}

// The bot's command_context logs "/<command> done guild=… channel=… user=…
// success=… elapsed_ms=…". Guild, channel and user IDs are matched but not
// captured, so they never leave this function.
var doneLine = regexp.MustCompile(
	`^/([a-z0-9-]+(?: [a-z0-9-]+)?) done guild=\S* channel=\S* user=\S* success=(True|False) elapsed_ms=(\d+)$`)

// ParseCommandLogs turns a Cloud Logging JSON export into command invocations.
// Entries that are not command completions are ignored.
func ParseCommandLogs(r io.Reader) (rows []store.Invocation, read int, err error) {
	var entries []logEntry
	if err := json.NewDecoder(r).Decode(&entries); err != nil {
		return nil, 0, fmt.Errorf("decode log export: %w", err)
	}
	for _, e := range entries {
		m := doneLine.FindStringSubmatch(e.JSONPayload.Message)
		if m == nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil {
			continue
		}
		elapsed, err := strconv.Atoi(m[3])
		if err != nil {
			continue
		}
		rows = append(rows, store.Invocation{
			SourceKey:  sourceKey(e),
			OccurredAt: ts,
			Command:    m[1],
			Success:    m[2] == "True",
			ElapsedMS:  elapsed,
		})
	}
	return rows, len(entries), nil
}

// sourceKey prefers Cloud Logging's insertId. The fallback hash covers
// hand-built exports; it includes the raw message, but only the digest is kept.
func sourceKey(e logEntry) string {
	if e.InsertID != "" {
		return "log:" + e.InsertID
	}
	sum := sha256.Sum256([]byte(e.Timestamp + "\x00" + e.JSONPayload.Message))
	return "sha256:" + hex.EncodeToString(sum[:])
}
