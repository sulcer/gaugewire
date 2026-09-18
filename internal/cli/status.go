package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

// runStatus prints the offline view: state.json plus spool counts. No network.
func runStatus(stdout io.Writer, now time.Time, zone *time.Location) error {
	home, err := store.Home()
	if err != nil {
		return err
	}
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	state, err := store.LoadState(home)
	if errors.Is(err, store.ErrStateCorrupt) {
		if _, werr := io.WriteString(stdout, "state.json is not valid; showing a fresh state\n"); werr != nil {
			return werr
		}
	} else if err != nil {
		return err
	}
	pending, dead, err := store.Counts(home)
	if err != nil {
		return err
	}
	entries, _, err := store.ListDeadLetters(home)
	if err != nil {
		return err
	}
	newest := ""
	if len(entries) > 0 {
		newest = entries[0].Reason
	}
	_, err = io.WriteString(stdout, renderStatus(cfg, state, pending, dead, newest, now, zone))
	return err
}

// renderStatus renders the offline view. newest is the reason of the newest
// dead letter, or empty when there is none.
func renderStatus(cfg config.Config, state store.State, pending, dead int, newest string, now time.Time, zone *time.Location) string {
	var b strings.Builder
	b.WriteString("Gaugewire\n\n")
	fmt.Fprintf(&b, "Node:     %s\nAccount:  %s\n\n", cfg.Node.Alias, cfg.Account.Alias)
	fmt.Fprintf(&b, "5h:       %s\n", windowLine(state.Windows.FiveHour, now, zone))
	fmt.Fprintf(&b, "7d:       %s\n\n", windowLine(state.Windows.SevenDay, now, zone))
	fmt.Fprintf(&b, "Last observation:  %s\n", ago(state.LastObservedAt, now))
	var publishedAt *time.Time
	if state.LastPublished != nil {
		publishedAt = &state.LastPublished.CapturedAt
	}
	fmt.Fprintf(&b, "Last publish:      %s\n\n", ago(publishedAt, now))
	suffix := ""
	if newest != "" {
		suffix = " · newest: " + newest
	}
	fmt.Fprintf(&b, "Pending events:    %d\n", pending)
	fmt.Fprintf(&b, "Dead letters:      %d%s\n\n", dead, suffix)
	for _, s := range cfg.Sinks {
		if !s.Enabled {
			continue
		}
		fmt.Fprintf(&b, "%-18s %s\n", s.ID+":", flushLine(state.LastFlush, now))
	}
	return b.String()
}

func windowLine(w quota.Window, now time.Time, zone *time.Location) string {
	switch w.Status {
	case quota.WindowObserved:
		if w.UsedPercentage == nil || w.ResetsAt == nil {
			return "unknown"
		}
		percent := strconv.FormatFloat(*w.UsedPercentage, 'f', -1, 64) + "%"
		return fmt.Sprintf("%-10s Reset:  %s", percent, resetLabel(*w.ResetsAt, now, zone))
	case quota.WindowExpired:
		if w.ResetsAt == nil {
			return "expired"
		}
		return fmt.Sprintf("%-10s Reset:  %s (passed)", "expired", resetLabel(*w.ResetsAt, now, zone))
	case quota.WindowUnknown:
		return "unknown"
	}
	return "unknown"
}

func resetLabel(at, now time.Time, zone *time.Location) string {
	local := at.In(zone)
	if local.Year() == now.In(zone).Year() && local.YearDay() == now.In(zone).YearDay() {
		return local.Format("15:04")
	}
	return local.Format("Jan 2 15:04")
}

func ago(at *time.Time, now time.Time) string {
	if at == nil {
		return "never"
	}
	d := now.Sub(*at)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func flushLine(record *store.FlushRecord, now time.Time) string {
	if record == nil {
		return "never flushed"
	}
	if record.OK {
		return "last flush ok " + ago(&record.At, now)
	}
	return "last flush failed " + ago(&record.At, now) + ": " + record.Error
}
