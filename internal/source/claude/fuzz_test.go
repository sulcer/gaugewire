package claude

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func FuzzParse(f *testing.F) {
	names := []string{"full.json", "no-rate-limits.json", "malformed-percentage.json", "malformed-reset.json", "old-version.json"}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "statusline", name))
		if err != nil {
			f.Fatalf("read fixture: %v", err)
		}
		f.Add(data)
	}
	f.Add([]byte(`{"version":"2.1.274","rate_limits":{"five_hour":{"used_percentage":-1,"resets_at":0}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		obs, issues, err := Parse(bytes.NewReader(data), time.Unix(0, 0).UTC())
		if err != nil {
			return
		}
		if obs.FiveHour != nil && (obs.FiveHour.UsedPercentage < 0 || obs.FiveHour.UsedPercentage > 100 || obs.FiveHour.ResetsAt.Unix() <= 0) {
			t.Fatalf("invalid five hour reading accepted: %+v", obs.FiveHour)
		}
		if obs.SevenDay != nil && (obs.SevenDay.UsedPercentage < 0 || obs.SevenDay.UsedPercentage > 100 || obs.SevenDay.ResetsAt.Unix() <= 0) {
			t.Fatalf("invalid seven day reading accepted: %+v", obs.SevenDay)
		}
		if len(issues) > 2 {
			t.Fatalf("more issues than windows: %v", issues)
		}
	})
}
