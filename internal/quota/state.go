package quota

import "time"

// SchemaVersion is the version of State and Snapshot; a breaking change bumps it.
const SchemaVersion = 1

// State is the machine-wide quota state persisted between invocations.
type State struct {
	SchemaVersion     int        `json:"schemaVersion"`
	Windows           Windows    `json:"windows"`
	LastObservedAt    *time.Time `json:"lastObservedAt"`
	ClaudeCodeVersion string     `json:"claudeCodeVersion"`
	LastPublished     *Published `json:"lastPublished"`
}

// Published records the snapshot most recently spooled for delivery.
type Published struct {
	EventID    string    `json:"eventId"`
	CapturedAt time.Time `json:"capturedAt"`
	Windows    Windows   `json:"windows"`
}

// NewState returns the state of a machine that has never observed anything.
func NewState() State {
	return State{
		SchemaVersion: SchemaVersion,
		Windows: Windows{
			FiveHour: Window{Status: WindowUnknown},
			SevenDay: Window{Status: WindowUnknown},
		},
	}
}

// MarkPublished records that a snapshot with the current windows was spooled.
func MarkPublished(state State, eventID string, capturedAt time.Time) State {
	state.LastPublished = &Published{EventID: eventID, CapturedAt: capturedAt.UTC().Truncate(time.Millisecond), Windows: state.Windows}
	return state
}
