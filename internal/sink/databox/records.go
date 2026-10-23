package databox

import (
	"strings"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
)

// Titles and primary keys of the resources bootstrap creates.
const (
	DataSourceTitle   = "Gaugewire"
	HistoryTitle      = "Claude Quota History"
	CurrentTitle      = "Claude Quota Current"
	HistoryPrimaryKey = "event_id"
	CurrentPrimaryKey = "account_id"
)

const timestampLayout = "2006-01-02T15:04:05.000Z07:00"

// HistorySchema declares the History dataset's columns, in the order the spec
// lists them.
func HistorySchema() []Column {
	return schema("event_id", "event_type", "account_id", "account_alias", "node_id", "node_alias",
		"platform", "captured_at", "published_at",
		"five_hour_status", "five_hour_used_percentage", "five_hour_resets_at",
		"seven_day_status", "seven_day_used_percentage", "seven_day_resets_at",
		"source_type", "claude_code_version", "observer_version")
}

// CurrentSchema declares the Current dataset's columns, in the order the spec
// lists them.
func CurrentSchema() []Column {
	return schema("account_id", "account_alias", "node_id", "node_alias", "platform", "latest_event_id",
		"captured_at", "last_seen_at",
		"five_hour_status", "five_hour_used_percentage", "five_hour_resets_at",
		"seven_day_status", "seven_day_used_percentage", "seven_day_resets_at",
		"claude_code_version", "observer_version")
}

// schema types the columns by name: *_at columns hold RFC 3339 timestamps,
// *_used_percentage columns hold numbers, and everything else is a string.
func schema(columns ...string) []Column {
	out := make([]Column, 0, len(columns))
	for _, id := range columns {
		dataType := "string"
		switch {
		case strings.HasSuffix(id, "_at"):
			dataType = "datetime"
		case strings.HasSuffix(id, "_used_percentage"):
			dataType = "number"
		}
		out = append(out, Column{ID: id, DataType: dataType})
	}
	return out
}

// HistoryRecord is one row of the History dataset: the whole snapshot plus
// the event type and the send time. Every column is present; unknown values
// are null.
func HistoryRecord(s quota.Snapshot, eventType quota.EventType, publishedAt time.Time) map[string]any {
	r := map[string]any{
		"event_id":     s.EventID,
		"event_type":   string(eventType),
		"published_at": timestamp(publishedAt),
		"source_type":  s.Source.Type,
	}
	for k, v := range common(s) {
		r[k] = v
	}
	return r
}

// CurrentRecord is the one row per subscription in the Current dataset.
func CurrentRecord(s quota.Snapshot, lastSeenAt time.Time) map[string]any {
	r := map[string]any{
		"latest_event_id": s.EventID,
		"last_seen_at":    timestamp(lastSeenAt),
	}
	for k, v := range common(s) {
		r[k] = v
	}
	return r
}

func common(s quota.Snapshot) map[string]any {
	r := map[string]any{
		"account_id":          s.Account.ID,
		"account_alias":       s.Account.Alias,
		"node_id":             s.Node.ID,
		"node_alias":          s.Node.Alias,
		"platform":            s.Node.Platform,
		"captured_at":         timestamp(s.CapturedAt),
		"claude_code_version": nullable(s.Source.ClaudeCodeVersion),
		"observer_version":    s.ObserverVersion,
	}
	window(r, "five_hour", s.Windows.FiveHour)
	window(r, "seven_day", s.Windows.SevenDay)
	return r
}

func window(r map[string]any, prefix string, w quota.Window) {
	r[prefix+"_status"] = string(w.Status)
	r[prefix+"_used_percentage"] = nil
	r[prefix+"_resets_at"] = nil
	if w.UsedPercentage != nil {
		r[prefix+"_used_percentage"] = *w.UsedPercentage
	}
	if w.ResetsAt != nil {
		r[prefix+"_resets_at"] = timestamp(*w.ResetsAt)
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func timestamp(t time.Time) string {
	return t.UTC().Format(timestampLayout)
}
