package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"uuid"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/sink"
	"github.com/sulcer/gaugewire/internal/sink/databox"
	"github.com/sulcer/gaugewire/internal/store"
)

// ErrChooseAccount means the key reaches several accounts and none was named.
var ErrChooseAccount = errors.New("several accounts are reachable; pass --account-id")

const defaultSinkID = "databox-main"

type bootstrapOptions struct {
	accountID  int64
	apiKeyFile string
	testIngest bool
	baseURL    string
	sinkID     string
	getenv     func(string) string
	now        func() time.Time
	httpClient *http.Client
	info       BuildInfo
}

func runDatabox(ctx context.Context, args []string, info BuildInfo, streams IO) error {
	if len(args) == 0 || args[0] != "bootstrap" {
		return fmt.Errorf("databox: expected the bootstrap subcommand: %w", ErrUsage)
	}
	flags := flag.NewFlagSet("databox bootstrap", flag.ContinueOnError)
	flags.SetOutput(streams.Stderr)
	opts := bootstrapOptions{getenv: os.Getenv, now: time.Now, info: info}
	flags.Int64Var(&opts.accountID, "account-id", 0, "account to use when the key reaches several")
	flags.StringVar(&opts.apiKeyFile, "api-key-file", "", "file holding the API key; its path is recorded in config.json")
	flags.BoolVar(&opts.testIngest, "test-ingest", false, "send one heartbeat event after bootstrapping")
	flags.StringVar(&opts.baseURL, "base-url", databox.DefaultBaseURL, "API base URL")
	flags.StringVar(&opts.sinkID, "sink-id", defaultSinkID, "sink id in config.json")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	home, err := store.Home()
	if err != nil {
		return err
	}
	return bootstrap(ctx, home, opts, streams.Stdout, streams.Stderr)
}

// bootstrap validates the key, picks the account, reuses or creates the data
// source and datasets, and records their ids in the sink entry. Repeated runs
// create nothing. The ids are always resolved by title, so an id in config.json
// that the account no longer holds is replaced rather than trusted.
func bootstrap(ctx context.Context, home string, opts bootstrapOptions, stdout, stderr io.Writer) error {
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	if err = store.EnsureLayout(home); err != nil {
		return err
	}
	entry := sinkEntry(&cfg, opts.sinkID)
	if err = resolveCredentials(entry, opts.apiKeyFile); err != nil {
		return err
	}
	key, warn, err := loadAPIKey(entry.Credentials, opts.getenv)
	if err != nil {
		return err
	}
	if warn != "" {
		fmt.Fprintln(stderr, "warning: "+warn)
	}
	client, err := databox.NewClient(opts.baseURL, key, opts.httpClient)
	if err != nil {
		return err
	}
	if err = client.ValidateKey(ctx); err != nil {
		return fmt.Errorf("validate key: %w", err)
	}
	account, err := chooseAccount(ctx, client, opts.accountID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "account:          %s (%d)\n", account.Name, account.ID)
	source, sourceCreated, err := ensureDataSource(ctx, client, account.ID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "data source:      %s (%d) %s\n", source.Title, source.ID, createdOrReused(sourceCreated))
	existing, err := client.Datasets(ctx, source.ID)
	if err != nil {
		return fmt.Errorf("list datasets: %w", err)
	}
	history, historyCreated, err := ensureDataset(ctx, client, existing, source.ID, databox.HistoryTitle, databox.HistoryPrimaryKey)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "history dataset:  %s %s\n", history.ID, createdOrReused(historyCreated))
	current, currentCreated, err := ensureDataset(ctx, client, existing, source.ID, databox.CurrentTitle, databox.CurrentPrimaryKey)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "current dataset:  %s %s\n", current.ID, createdOrReused(currentCreated))
	entry.Type = config.SinkTypeDatabox
	entry.Enabled = true
	entry.BaseURL = opts.baseURL
	entry.AccountID = account.ID
	entry.DataSourceID = source.ID
	entry.HistoryDatasetID = history.ID
	entry.CurrentDatasetID = current.ID
	if err = config.Save(home, cfg); err != nil {
		return err
	}
	if !opts.testIngest {
		return nil
	}
	return sendTestHeartbeat(ctx, home, cfg, *entry, client, opts, stdout)
}

// sinkEntry returns the sink with the id, appending a new one when absent. The
// returned pointer stays valid because nothing appends to cfg.Sinks afterwards.
func sinkEntry(cfg *config.Config, id string) *config.Sink {
	for i := range cfg.Sinks {
		if cfg.Sinks[i].ID == id {
			return &cfg.Sinks[i]
		}
	}
	cfg.Sinks = append(cfg.Sinks, config.Sink{ID: id, Type: config.SinkTypeDatabox})
	return &cfg.Sinks[len(cfg.Sinks)-1]
}

// resolveCredentials records where the key is read from. A named key file wins
// and is stored as an absolute path; the file itself is never copied or read
// into config.json.
func resolveCredentials(entry *config.Sink, apiKeyFile string) error {
	if apiKeyFile != "" {
		absolute, err := filepath.Abs(apiKeyFile)
		if err != nil {
			return fmt.Errorf("resolve key file path: %w", err)
		}
		entry.Credentials = config.Credentials{APIKeyFile: absolute}
		return nil
	}
	if entry.Credentials.APIKeyFile == "" && entry.Credentials.APIKeyEnv == "" {
		entry.Credentials.APIKeyEnv = config.DefaultAPIKeyEnv
	}
	return nil
}

func chooseAccount(ctx context.Context, client *databox.Client, wanted int64) (databox.Account, error) {
	accounts, err := client.Accounts(ctx)
	if err != nil {
		return databox.Account{}, fmt.Errorf("list accounts: %w", err)
	}
	switch {
	case len(accounts) == 0:
		return databox.Account{}, errors.New("the key reaches no account")
	case wanted != 0:
		for _, a := range accounts {
			if a.ID == wanted {
				return a, nil
			}
		}
		return databox.Account{}, fmt.Errorf("account %d is not reachable with this key", wanted)
	case len(accounts) == 1:
		return accounts[0], nil
	default:
		return databox.Account{}, ErrChooseAccount
	}
}

func ensureDataSource(ctx context.Context, client *databox.Client, accountID int64) (databox.DataSource, bool, error) {
	sources, err := client.DataSources(ctx, accountID)
	if err != nil {
		return databox.DataSource{}, false, fmt.Errorf("list data sources: %w", err)
	}
	for _, s := range sources {
		if s.Title == databox.DataSourceTitle {
			return s, false, nil
		}
	}
	created, err := client.CreateDataSource(ctx, accountID, databox.DataSourceTitle, "UTC")
	if err != nil {
		return databox.DataSource{}, false, fmt.Errorf("create data source: %w", err)
	}
	if created.ID == 0 {
		return databox.DataSource{}, false, errors.New("databox returned no id for the created data source")
	}
	return created, true, nil
}

// ensureDataset picks the dataset with the title out of the data source's
// datasets, creating it when it is absent. The list is read once by the caller
// because both datasets live in the same data source.
func ensureDataset(ctx context.Context, client *databox.Client, existing []databox.Dataset, dataSourceID int64, title, primaryKey string) (databox.Dataset, bool, error) {
	for _, d := range existing {
		if d.Title == title {
			return d, false, nil
		}
	}
	created, err := client.CreateDataset(ctx, dataSourceID, title, []string{primaryKey})
	if err != nil {
		return databox.Dataset{}, false, fmt.Errorf("create dataset %q: %w", title, err)
	}
	if created.ID == "" {
		return databox.Dataset{}, false, fmt.Errorf("databox returned no id for the created dataset %q", title)
	}
	return created, true, nil
}

func createdOrReused(created bool) string {
	if created {
		return "created"
	}
	return "reused"
}

// sendTestHeartbeat sends one heartbeat built from the current state and prints
// the ingestion ids, so the dashboard shows a row before the next quota change.
// Without an observed window the heartbeat would be all nulls, so it is skipped
// until Claude Code has shown its status line. The ids are read back from
// state.json and must carry this send's time: an ingestion the sink could not
// record, or a record left by an earlier run, is reported as a failure, because
// the next flush would resend it.
func sendTestHeartbeat(ctx context.Context, home string, cfg config.Config, entry config.Sink, client *databox.Client, opts bootstrapOptions, stdout io.Writer) error {
	state, err := store.LoadState(home)
	if err != nil && !errors.Is(err, store.ErrStateCorrupt) {
		return err
	}
	if state.Windows.FiveHour.Status != quota.WindowObserved && state.Windows.SevenDay.Status != quota.WindowObserved {
		fmt.Fprintln(stdout, "test ingest:      skipped: no quota observation yet; run it again after Claude Code has shown its status line")
		return nil
	}
	logger, closeLog := openLogger(home)
	defer closeLog()
	sentAt := opts.now().UTC()
	ingestions := sink.StateIngestions{Home: home}
	s, err := databox.New(entry.ID, client, databox.Datasets{History: entry.HistoryDatasetID, Current: entry.CurrentDatasetID}, ingestions, func() time.Time { return sentAt }, logger)
	if err != nil {
		return err
	}
	snapshot := quota.NewSnapshot(cfg.QuotaIdentity(runtime.GOOS, opts.info.Version), state.State, uuid.NewV4().String(), sentAt)
	if err = s.PublishBatch(ctx, []sink.Delivery{{EventType: quota.EventHeartbeat, Snapshot: snapshot}}); err != nil {
		return fmt.Errorf("test ingest: %w", err)
	}
	ing, found, err := ingestions.LoadIngestion(ctx, entry.ID)
	if err != nil {
		return err
	}
	if !found || !ing.At.Equal(sentAt) {
		return errors.New("test ingest was accepted but the ingestion record could not be read back; check logs/gaugewire.log")
	}
	fmt.Fprintf(stdout, "test ingest:      history %s, current %s\n", ing.History, ing.Current)
	return nil
}
