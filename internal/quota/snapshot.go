package quota

import "time"

// SourceTypeClaudeCodeStatusline names the only v1 source.
const SourceTypeClaudeCodeStatusline = "claude-code-statusline"

// Identity is the configured node and account identity; it is never inferred.
type Identity struct {
	NodeID          string
	NodeAlias       string
	Platform        string
	AccountID       string
	AccountAlias    string
	ObserverVersion string
}

// Node identifies the machine.
type Node struct {
	ID       string `json:"id"`
	Alias    string `json:"alias"`
	Platform string `json:"platform"`
}

// Account identifies the Claude subscription logged into the node.
type Account struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
}

// Source says where the observation came from.
type Source struct {
	Type              string `json:"type"`
	ClaudeCodeVersion string `json:"claudeCodeVersion"`
}

// Snapshot is QuotaSnapshot v1, the only shape that leaves the machine.
type Snapshot struct {
	SchemaVersion   int       `json:"schemaVersion"`
	EventID         string    `json:"eventId"`
	Node            Node      `json:"node"`
	Account         Account   `json:"account"`
	CapturedAt      time.Time `json:"capturedAt"`
	Windows         Windows   `json:"windows"`
	Source          Source    `json:"source"`
	ObserverVersion string    `json:"observerVersion"`
}

// NewSnapshot builds the snapshot for the current state.
func NewSnapshot(id Identity, state State, eventID string, capturedAt time.Time) Snapshot {
	return Snapshot{
		SchemaVersion:   SchemaVersion,
		EventID:         eventID,
		Node:            Node{ID: id.NodeID, Alias: id.NodeAlias, Platform: id.Platform},
		Account:         Account{ID: id.AccountID, Alias: id.AccountAlias},
		CapturedAt:      capturedAt.UTC(),
		Windows:         state.Windows,
		Source:          Source{Type: SourceTypeClaudeCodeStatusline, ClaudeCodeVersion: state.ClaudeCodeVersion},
		ObserverVersion: id.ObserverVersion,
	}
}
