package store

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/usageservice/usage"
	log "github.com/sirupsen/logrus"
)

type Setup struct {
	CPAUpstreamURL string `json:"cpaBaseUrl"`
	ManagementKey  string `json:"managementKey,omitempty"`
	Queue          string `json:"queue,omitempty"`
	PopSide        string `json:"popSide,omitempty"`
}

type ManagerConfig struct {
	CPAConnection        ManagerCPAConnectionConfig        `json:"cpaConnection"`
	Collector            ManagerCollectorConfig            `json:"collector"`
	ExternalUsageService ManagerExternalUsageServiceConfig `json:"externalUsageService"`
	UpdatedAtMS          int64                             `json:"updatedAtMs,omitempty"`
}

type ManagerCPAConnectionConfig struct {
	CPABaseURL    string `json:"cpaBaseUrl"`
	ManagementKey string `json:"managementKey,omitempty"`
}

type ManagerCollectorConfig struct {
	Enabled        *bool  `json:"enabled,omitempty"`
	CollectorMode  string `json:"collectorMode,omitempty"`
	Queue          string `json:"queue,omitempty"`
	PopSide        string `json:"popSide,omitempty"`
	BatchSize      int    `json:"batchSize,omitempty"`
	PollIntervalMS int    `json:"pollIntervalMs,omitempty"`
	QueryLimit     int    `json:"queryLimit,omitempty"`
	TLSSkipVerify  bool   `json:"tlsSkipVerify,omitempty"`
}

type ManagerExternalUsageServiceConfig struct {
	Enabled     bool   `json:"enabled"`
	ServiceBase string `json:"serviceBase,omitempty"`
}

type InsertResult struct {
	Inserted int `json:"inserted"`
	Skipped  int `json:"skipped"`
}

type ModelPrice struct {
	Prompt        float64 `json:"prompt"`
	Completion    float64 `json:"completion"`
	Cache         float64 `json:"cache"`
	Source        string  `json:"source,omitempty"`
	SourceModelID string  `json:"sourceModelId,omitempty"`
	RawJSON       string  `json:"rawJson,omitempty"`
	UpdatedAtMS   int64   `json:"updatedAtMs,omitempty"`
	SyncedAtMS    *int64  `json:"syncedAtMs,omitempty"`
}

type ModelPriceSyncResult struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

type APIKeyAlias struct {
	APIKeyHash  string `json:"apiKeyHash"`
	Alias       string `json:"alias"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}

type Store struct {
	db *sql.DB
}

const managerConfigKey = "manager_config_v1"

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		backupPath := "cpa-manager-database-backup.tar.gz"
		if _, errBackup := os.Stat(backupPath); errBackup == nil {
			log.Infof("Auto-extracting historical database from %s to %s", backupPath, filepath.Dir(path))
			if errExt := extractBackup(backupPath, filepath.Dir(path)); errExt != nil {
				log.Errorf("Failed to extract historical database: %v", errExt)
			}
		} else {
			if execPath, errExec := os.Executable(); errExec == nil {
				execBackupPath := filepath.Join(filepath.Dir(execPath), "cpa-manager-database-backup.tar.gz")
				if _, errExecBackup := os.Stat(execBackupPath); errExecBackup == nil {
					log.Infof("Auto-extracting historical database from %s to %s", execBackupPath, filepath.Dir(path))
					if errExt := extractBackup(execBackupPath, filepath.Dir(path)); errExt != nil {
						log.Errorf("Failed to extract historical database: %v", errExt)
					}
				}
			}
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func extractBackup(tarGzPath, destDir string) error {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		name := filepath.Base(hdr.Name)
		if name != "usage.sqlite" && name != "usage.sqlite-shm" && name != "usage.sqlite-wal" {
			continue
		}

		destPath := filepath.Join(destDir, name)
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, hdr.FileInfo().Mode())
		if err != nil {
			return err
		}

		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		out.Close()
	}
	return nil
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) init() error {
	statements := []string{
		`pragma journal_mode = WAL`,
		`pragma synchronous = NORMAL`,
		`pragma busy_timeout = 5000`,
		`pragma cache_size = -64000`,
		`pragma mmap_size = 268435456`,
		`pragma foreign_keys = ON`,
		`create table if not exists usage_events (
			id integer primary key autoincrement,
			request_id text,
			event_hash text not null unique,
			timestamp_ms integer not null,
			timestamp text not null,
			provider text,
			model text not null,
			endpoint text,
			method text,
			path text,
			auth_type text,
			auth_index text,
			source text,
			source_hash text,
			api_key_hash text,
			account_snapshot text,
			auth_label_snapshot text,
			auth_file_snapshot text,
			auth_provider_snapshot text,
			auth_snapshot_at_ms integer,
			input_tokens integer not null default 0,
			output_tokens integer not null default 0,
			reasoning_tokens integer not null default 0,
			cached_tokens integer not null default 0,
			cache_tokens integer not null default 0,
			total_tokens integer not null default 0,
			latency_ms integer,
			failed integer not null default 0,
			raw_json text,
			created_at_ms integer not null
		)`,
		`create index if not exists idx_usage_events_timestamp on usage_events(timestamp_ms)`,
		`create index if not exists idx_usage_events_request_id on usage_events(request_id)`,
		`create index if not exists idx_usage_events_model on usage_events(model)`,
		`create index if not exists idx_usage_events_auth_index on usage_events(auth_index)`,
		`create index if not exists idx_usage_events_endpoint on usage_events(endpoint)`,
		`create index if not exists idx_usage_events_prov_auth_fail on usage_events(provider, auth_index, failed)`,
		`create index if not exists idx_usage_events_time_model on usage_events(timestamp_ms, model)`,
		`create table if not exists usage_daily_stats (
			stat_date text not null,
			provider text not null,
			auth_index text not null,
			model text not null,
			total_requests integer not null default 0,
			success_count integer not null default 0,
			failed_count integer not null default 0,
			input_tokens integer not null default 0,
			output_tokens integer not null default 0,
			reasoning_tokens integer not null default 0,
			cached_tokens integer not null default 0,
			total_tokens integer not null default 0,
			total_latency_ms integer not null default 0,
			updated_at_ms integer not null default 0,
			primary key (stat_date, provider, auth_index, model)
		)`,
		`create index if not exists idx_usage_daily_stats_prov_auth on usage_daily_stats(provider, auth_index)`,
		`create index if not exists idx_usage_daily_stats_date on usage_daily_stats(stat_date)`,
		`create table if not exists dead_letter_events (
			id integer primary key autoincrement,
			payload text not null,
			error text not null,
			created_at_ms integer not null
		)`,
		`create table if not exists settings (
			key text primary key,
			value text not null,
			updated_at_ms integer not null
		)`,
		`create table if not exists model_prices (
			model text primary key,
			prompt_per_1m real not null,
			completion_per_1m real not null,
			cache_per_1m real not null,
			source text,
			source_model_id text,
			raw_json text,
			updated_at_ms integer not null,
			synced_at_ms integer
		)`,
		`create table if not exists api_key_aliases (
			api_key_hash text primary key,
			alias text not null,
			updated_at_ms integer not null
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	if err := s.ensureUsageEventSnapshotColumns(); err != nil {
		return err
	}
	if err := s.migrateHistoricalDailyStats(); err != nil {
		log.WithError(err).Warn("[usage-store] migrateHistoricalDailyStats warning")
	}
	return nil
}

func (s *Store) migrateHistoricalDailyStats() error {
	var val string
	err := s.db.QueryRow(`select value from settings where key = 'daily_stats_migrated_v1'`).Scan(&val)
	if err == nil && val != "" {
		return nil
	}

	const migrationSQL = `
	insert into usage_daily_stats (
		stat_date, provider, auth_index, model,
		total_requests, success_count, failed_count,
		input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
		total_latency_ms, updated_at_ms
	)
	select 
		case 
			when length(timestamp) >= 10 then substr(timestamp, 1, 10)
			else strftime('%Y-%m-%d', timestamp_ms / 1000, 'unixepoch')
		end as stat_date,
		coalesce(nullif(provider, ''), 'unknown') as provider,
		coalesce(nullif(auth_index, ''), 'unknown') as auth_index,
		coalesce(nullif(model, ''), 'unknown') as model,
		count(*) as total_requests,
		sum(case when failed = 0 then 1 else 0 end) as success_count,
		sum(case when failed = 1 then 1 else 0 end) as failed_count,
		sum(input_tokens) as input_tokens,
		sum(output_tokens) as output_tokens,
		sum(reasoning_tokens) as reasoning_tokens,
		sum(cached_tokens + cache_tokens) as cached_tokens,
		sum(total_tokens) as total_tokens,
		sum(coalesce(latency_ms, 0)) as total_latency_ms,
		strftime('%s', 'now') * 1000 as updated_at_ms
	from usage_events
	group by stat_date, provider, auth_index, model
	on conflict (stat_date, provider, auth_index, model) do update set
		total_requests = excluded.total_requests,
		success_count = excluded.success_count,
		failed_count = excluded.failed_count,
		input_tokens = excluded.input_tokens,
		output_tokens = excluded.output_tokens,
		reasoning_tokens = excluded.reasoning_tokens,
		cached_tokens = excluded.cached_tokens,
		total_tokens = excluded.total_tokens,
		total_latency_ms = excluded.total_latency_ms,
		updated_at_ms = excluded.updated_at_ms;`

	if _, err := s.db.Exec(migrationSQL); err != nil {
		return fmt.Errorf("daily stats historical migration failed: %w", err)
	}

	nowMS := time.Now().UnixMilli()
	_, _ = s.db.Exec(`insert or replace into settings (key, value, updated_at_ms) values ('daily_stats_migrated_v1', 'true', ?)`, nowMS)
	log.Infof("[usage-store] successfully migrated historical usage events into usage_daily_stats")
	return nil
}

func (s *Store) ensureUsageEventSnapshotColumns() error {
	rows, err := s.db.Query(`pragma table_info(usage_events)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	columns := []struct {
		name       string
		definition string
	}{
		{name: "account_snapshot", definition: "text"},
		{name: "auth_label_snapshot", definition: "text"},
		{name: "auth_file_snapshot", definition: "text"},
		{name: "auth_provider_snapshot", definition: "text"},
		{name: "auth_project_id_snapshot", definition: "text"},
		{name: "auth_snapshot_at_ms", definition: "integer"},
		{name: "requested_model", definition: "text"},
		{name: "resolved_model", definition: "text"},
	}
	for _, column := range columns {
		if _, ok := existing[column.name]; ok {
			continue
		}
		if _, err := s.db.Exec(fmt.Sprintf(
			`alter table usage_events add column %s %s`,
			column.name,
			column.definition,
		)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveSetup(ctx context.Context, setup Setup) error {
	if setup.CPAUpstreamURL == "" || setup.ManagementKey == "" {
		return errors.New("cpaBaseUrl and managementKey are required")
	}
	data, err := json.Marshal(setup)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(
		ctx,
		`insert into settings(key, value, updated_at_ms)
		 values('setup', ?, ?)
		 on conflict(key) do update set value = excluded.value, updated_at_ms = excluded.updated_at_ms`,
		string(data),
		time.Now().UnixMilli(),
	)
	return err
}

func (s *Store) LoadSetup(ctx context.Context) (Setup, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `select value from settings where key = 'setup'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Setup{}, false, nil
	}
	if err != nil {
		return Setup{}, false, err
	}
	var setup Setup
	if err := json.Unmarshal([]byte(raw), &setup); err != nil {
		return Setup{}, false, err
	}
	return setup, true, nil
}

func (s *Store) SaveManagerConfig(ctx context.Context, cfg ManagerConfig) error {
	cfg.UpdatedAtMS = time.Now().UnixMilli()
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(
		ctx,
		`insert into settings(key, value, updated_at_ms)
		 values(?, ?, ?)
		 on conflict(key) do update set value = excluded.value, updated_at_ms = excluded.updated_at_ms`,
		managerConfigKey,
		string(data),
		cfg.UpdatedAtMS,
	)
	return err
}

func (s *Store) LoadManagerConfig(ctx context.Context) (ManagerConfig, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `select value from settings where key = ?`, managerConfigKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ManagerConfig{}, false, nil
	}
	if err != nil {
		return ManagerConfig{}, false, err
	}
	var cfg ManagerConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return ManagerConfig{}, false, err
	}
	return cfg, true, nil
}

func (s *Store) LoadModelPrices(ctx context.Context) (map[string]ModelPrice, error) {
	rows, err := s.db.QueryContext(ctx, `select
		model, prompt_per_1m, completion_per_1m, cache_per_1m, source, source_model_id, raw_json,
		updated_at_ms, synced_at_ms
		from model_prices order by model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	prices := map[string]ModelPrice{}
	for rows.Next() {
		var model string
		var price ModelPrice
		var source, sourceModelID, rawJSON sql.NullString
		var syncedAt sql.NullInt64
		if err := rows.Scan(
			&model,
			&price.Prompt,
			&price.Completion,
			&price.Cache,
			&source,
			&sourceModelID,
			&rawJSON,
			&price.UpdatedAtMS,
			&syncedAt,
		); err != nil {
			return nil, err
		}
		price.Source = source.String
		price.SourceModelID = sourceModelID.String
		price.RawJSON = rawJSON.String
		if syncedAt.Valid {
			value := syncedAt.Int64
			price.SyncedAtMS = &value
		}
		prices[model] = price
	}
	return prices, rows.Err()
}

func (s *Store) SaveModelPrices(ctx context.Context, prices map[string]ModelPrice) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `delete from model_prices`); err != nil {
		return err
	}
	if len(prices) == 0 {
		return tx.Commit()
	}

	stmt, err := tx.PrepareContext(ctx, `insert into model_prices (
		model, prompt_per_1m, completion_per_1m, cache_per_1m, source, source_model_id,
		raw_json, updated_at_ms, synced_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UnixMilli()
	for model, price := range prices {
		if err := validateModelPrice(model, price); err != nil {
			return err
		}
		if _, err := stmt.ExecContext(
			ctx,
			model,
			price.Prompt,
			price.Completion,
			price.Cache,
			nullString(price.Source),
			nullString(price.SourceModelID),
			nullString(price.RawJSON),
			now,
			nullInt(price.SyncedAtMS),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertSyncedModelPrices(ctx context.Context, prices map[string]ModelPrice) (ModelPriceSyncResult, error) {
	if len(prices) == 0 {
		return ModelPriceSyncResult{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelPriceSyncResult{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, `insert into model_prices (
		model, prompt_per_1m, completion_per_1m, cache_per_1m, source, source_model_id,
		raw_json, updated_at_ms, synced_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(model) do update set
		prompt_per_1m = excluded.prompt_per_1m,
		completion_per_1m = excluded.completion_per_1m,
		cache_per_1m = excluded.cache_per_1m,
		source = excluded.source,
		source_model_id = excluded.source_model_id,
		raw_json = excluded.raw_json,
		updated_at_ms = excluded.updated_at_ms,
		synced_at_ms = excluded.synced_at_ms`)
	if err != nil {
		return ModelPriceSyncResult{}, err
	}
	defer stmt.Close()

	now := time.Now().UnixMilli()
	result := ModelPriceSyncResult{}
	for model, price := range prices {
		if err := validateModelPrice(model, price); err != nil {
			result.Skipped++
			continue
		}
		if price.Source == "" {
			price.Source = "sync"
		}
		if price.SourceModelID == "" {
			price.SourceModelID = model
		}
		price.UpdatedAtMS = now
		price.SyncedAtMS = &now
		if _, err := stmt.ExecContext(
			ctx,
			model,
			price.Prompt,
			price.Completion,
			price.Cache,
			nullString(price.Source),
			nullString(price.SourceModelID),
			nullString(price.RawJSON),
			now,
			now,
		); err != nil {
			return ModelPriceSyncResult{}, err
		}
		result.Imported++
	}
	if err := tx.Commit(); err != nil {
		return ModelPriceSyncResult{}, err
	}
	return result, nil
}

func (s *Store) LoadAPIKeyAliases(ctx context.Context) ([]APIKeyAlias, error) {
	rows, err := s.db.QueryContext(ctx, `select api_key_hash, alias, updated_at_ms
		from api_key_aliases
		order by alias collate nocase, api_key_hash`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	aliases := []APIKeyAlias{}
	for rows.Next() {
		var alias APIKeyAlias
		if err := rows.Scan(&alias.APIKeyHash, &alias.Alias, &alias.UpdatedAtMS); err != nil {
			return nil, err
		}
		aliases = append(aliases, alias)
	}
	return aliases, rows.Err()
}

// UpsertAPIKeyAliases 写入 / 更新 API Key 别名映射。
//
// activeHashes 表示当前配置中仍在使用的 API Key hash 集合：
//   - 非空时：别名唯一性校验只在「活跃集合 ∪ items 中的 hash」内做；若冲突方
//     是不在活跃集合中的孤儿 hash（例如删除 / 编辑密钥后的历史残留），会自动
//     清理该孤儿映射并把别名让渡给新的 hash。
//   - 为空 (nil) 时：保留旧行为，所有现有映射都视为活跃，遇到同名直接拒绝。
func (s *Store) UpsertAPIKeyAliases(ctx context.Context, aliases []APIKeyAlias, activeHashes []string) error {
	if len(aliases) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	normalizedAliases := make([]APIKeyAlias, 0, len(aliases))
	seenAliases := map[string]string{}
	for _, alias := range aliases {
		normalized, err := normalizeAPIKeyAlias(alias, now)
		if err != nil {
			return err
		}
		aliasKey := normalizeAPIKeyAliasUniqueKey(normalized.Alias)
		if existingHash, ok := seenAliases[aliasKey]; ok && existingHash != normalized.APIKeyHash {
			return errors.New("api key alias already exists")
		}
		seenAliases[aliasKey] = normalized.APIKeyHash
		normalizedAliases = append(normalizedAliases, normalized)
	}

	var activeSet map[string]struct{}
	if len(activeHashes) > 0 {
		activeSet = make(map[string]struct{}, len(activeHashes)+len(normalizedAliases))
		for _, h := range activeHashes {
			hash := strings.ToLower(strings.TrimSpace(h))
			if validAPIKeyHash(hash) {
				activeSet[hash] = struct{}{}
			}
		}
		for _, normalized := range normalizedAliases {
			activeSet[normalized.APIKeyHash] = struct{}{}
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, `insert into api_key_aliases (
		api_key_hash, alias, updated_at_ms
	) values (?, ?, ?)
	on conflict(api_key_hash) do update set
		alias = excluded.alias,
		updated_at_ms = excluded.updated_at_ms`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	deleteStmt, err := tx.PrepareContext(ctx, `delete from api_key_aliases where api_key_hash = ?`)
	if err != nil {
		return err
	}
	defer deleteStmt.Close()

	existingRows, err := tx.QueryContext(ctx, `select api_key_hash, alias from api_key_aliases`)
	if err != nil {
		return err
	}
	existingAliases := map[string]string{}
	for existingRows.Next() {
		var apiKeyHash string
		var alias string
		if err := existingRows.Scan(&apiKeyHash, &alias); err != nil {
			_ = existingRows.Close()
			return err
		}
		existingAliases[normalizeAPIKeyAliasUniqueKey(alias)] = apiKeyHash
	}
	if err := existingRows.Close(); err != nil {
		return err
	}
	if err := existingRows.Err(); err != nil {
		return err
	}

	for _, normalized := range normalizedAliases {
		aliasKey := normalizeAPIKeyAliasUniqueKey(normalized.Alias)
		if existingHash, ok := existingAliases[aliasKey]; ok && existingHash != normalized.APIKeyHash {
			if activeSet == nil {
				return errors.New("api key alias already exists")
			}
			if _, isActive := activeSet[existingHash]; isActive {
				return errors.New("api key alias already exists")
			}
			// 孤儿 hash 上的同名别名：先删除残留映射，再让渡给新 hash。
			if _, err := deleteStmt.ExecContext(ctx, existingHash); err != nil {
				return err
			}
			delete(existingAliases, aliasKey)
		}
		if _, err := stmt.ExecContext(
			ctx,
			normalized.APIKeyHash,
			normalized.Alias,
			normalized.UpdatedAtMS,
		); err != nil {
			return err
		}
		existingAliases[aliasKey] = normalized.APIKeyHash
	}
	return tx.Commit()
}

func (s *Store) DeleteAPIKeyAlias(ctx context.Context, apiKeyHash string) error {
	hash := strings.ToLower(strings.TrimSpace(apiKeyHash))
	if !validAPIKeyHash(hash) {
		return errors.New("valid apiKeyHash is required")
	}
	_, err := s.db.ExecContext(ctx, `delete from api_key_aliases where api_key_hash = ?`, hash)
	return err
}

func normalizeAPIKeyAlias(alias APIKeyAlias, now int64) (APIKeyAlias, error) {
	hash := strings.ToLower(strings.TrimSpace(alias.APIKeyHash))
	if !validAPIKeyHash(hash) {
		return APIKeyAlias{}, errors.New("valid apiKeyHash is required")
	}
	label := strings.TrimSpace(alias.Alias)
	if label == "" {
		return APIKeyAlias{}, errors.New("alias is required")
	}
	if len([]rune(label)) > 120 {
		return APIKeyAlias{}, errors.New("alias must be 120 characters or less")
	}
	if alias.UpdatedAtMS <= 0 {
		alias.UpdatedAtMS = now
	}
	alias.APIKeyHash = hash
	alias.Alias = label
	return alias, nil
}

func normalizeAPIKeyAliasUniqueKey(alias string) string {
	return strings.ToLower(strings.TrimSpace(alias))
}

func validAPIKeyHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}

func validateModelPrice(model string, price ModelPrice) error {
	if model == "" {
		return errors.New("model is required")
	}
	if !validPriceValue(price.Prompt) || !validPriceValue(price.Completion) || !validPriceValue(price.Cache) {
		return fmt.Errorf("invalid model price for %s", model)
	}
	return nil
}

func validPriceValue(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (s *Store) InsertEvents(ctx context.Context, events []usage.Event) (InsertResult, error) {
	if len(events) == 0 {
		return InsertResult{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InsertResult{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, `insert or ignore into usage_events (
		request_id, event_hash, timestamp_ms, timestamp, provider, model, endpoint, method, path,
		auth_type, auth_index, source, source_hash, api_key_hash,
		account_snapshot, auth_label_snapshot, auth_file_snapshot, auth_provider_snapshot, auth_project_id_snapshot, auth_snapshot_at_ms,
		requested_model, resolved_model,
		input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_tokens, total_tokens,
		latency_ms, failed, raw_json, created_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return InsertResult{}, err
	}
	defer stmt.Close()

	rollupStmt, err := tx.PrepareContext(ctx, `
		insert into usage_daily_stats (
			stat_date, provider, auth_index, model,
			total_requests, success_count, failed_count,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
			total_latency_ms, updated_at_ms
		) values (?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict (stat_date, provider, auth_index, model) do update set
			total_requests = total_requests + 1,
			success_count = success_count + excluded.success_count,
			failed_count = failed_count + excluded.failed_count,
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens,
			reasoning_tokens = reasoning_tokens + excluded.reasoning_tokens,
			cached_tokens = cached_tokens + excluded.cached_tokens,
			total_tokens = total_tokens + excluded.total_tokens,
			total_latency_ms = total_latency_ms + excluded.total_latency_ms,
			updated_at_ms = excluded.updated_at_ms
	`)
	if err != nil {
		return InsertResult{}, err
	}
	defer rollupStmt.Close()

	result := InsertResult{}
	nowMS := time.Now().UnixMilli()

	for _, event := range events {
		failed := 0
		if event.Failed {
			failed = 1
		}
		res, err := stmt.ExecContext(
			ctx,
			nullString(event.RequestID),
			event.EventHash,
			event.TimestampMS,
			event.Timestamp,
			nullString(event.Provider),
			event.Model,
			nullString(event.Endpoint),
			nullString(event.Method),
			nullString(event.Path),
			nullString(event.AuthType),
			nullString(event.AuthIndex),
			nullString(event.Source),
			nullString(event.SourceHash),
			nullString(event.APIKeyHash),
			nullString(event.AccountSnapshot),
			nullString(event.AuthLabelSnapshot),
			nullString(event.AuthFileSnapshot),
			nullString(event.AuthProviderSnapshot),
			nullString(event.AuthProjectIDSnapshot),
			nullPositiveInt64(event.AuthSnapshotAtMS),
			nullString(event.RequestedModel),
			nullString(event.ResolvedModel),
			event.InputTokens,
			event.OutputTokens,
			event.ReasoningTokens,
			event.CachedTokens,
			event.CacheTokens,
			event.TotalTokens,
			nullInt(event.LatencyMS),
			failed,
			nullString(event.RawJSON),
			event.CreatedAtMS,
		)
		if err != nil {
			return InsertResult{}, err
		}
		affected, _ := res.RowsAffected()
		if affected > 0 {
			result.Inserted++

			// Upsert to daily summary table for permanent lightweight rollups
			var statDate string
			if len(event.Timestamp) >= 10 {
				statDate = event.Timestamp[:10]
			} else if event.TimestampMS > 0 {
				statDate = time.UnixMilli(event.TimestampMS).UTC().Format("2006-01-02")
			} else {
				statDate = time.Now().UTC().Format("2006-01-02")
			}
			prov := strings.TrimSpace(event.Provider)
			if prov == "" {
				prov = "unknown"
			}
			authIdx := strings.TrimSpace(event.AuthIndex)
			if authIdx == "" {
				authIdx = "unknown"
			}
			mdl := strings.TrimSpace(event.Model)
			if mdl == "" {
				mdl = "unknown"
			}
			sucCount := int64(1 - failed)
			failCount := int64(failed)
			latency := int64(0)
			if event.LatencyMS != nil {
				latency = *event.LatencyMS
			}
			cachedToks := event.CachedTokens + event.CacheTokens

			if _, err := rollupStmt.ExecContext(
				ctx,
				statDate,
				prov,
				authIdx,
				mdl,
				sucCount,
				failCount,
				event.InputTokens,
				event.OutputTokens,
				event.ReasoningTokens,
				cachedToks,
				event.TotalTokens,
				latency,
				nowMS,
			); err != nil {
				return InsertResult{}, err
			}
		} else {
			result.Skipped++
		}
	}
	if err := tx.Commit(); err != nil {
		return InsertResult{}, err
	}
	return result, nil
}

func (s *Store) AddDeadLetter(ctx context.Context, payload string, parseErr error) error {
	_, err := s.db.ExecContext(
		ctx,
		`insert into dead_letter_events(payload, error, created_at_ms) values(?, ?, ?)`,
		payload,
		parseErr.Error(),
		time.Now().UnixMilli(),
	)
	return err
}

func (s *Store) RecentEvents(ctx context.Context, limit int) ([]usage.Event, error) {
	if limit <= 0 {
		limit = 50000
	}
	rows, err := s.db.QueryContext(ctx, `select
		request_id, event_hash, timestamp_ms, timestamp, provider, model, endpoint, method, path,
		auth_type, auth_index, source, source_hash, api_key_hash,
		account_snapshot, auth_label_snapshot, auth_file_snapshot, auth_provider_snapshot, auth_project_id_snapshot, auth_snapshot_at_ms,
		requested_model, resolved_model,
		input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_tokens, total_tokens,
		latency_ms, failed, raw_json, created_at_ms
		from usage_events
		order by timestamp_ms desc, id desc
		limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]usage.Event, 0)
	for rows.Next() {
		var event usage.Event
		var requestID, provider, endpoint, method, path, authType, authIndex, source, sourceHash, apiKeyHash, accountSnapshot, authLabelSnapshot, authFileSnapshot, authProviderSnapshot, authProjectIDSnapshot, requestedModel, resolvedModel, rawJSON sql.NullString
		var authSnapshotAt sql.NullInt64
		var latency sql.NullInt64
		var failed int
		if err := rows.Scan(
			&requestID,
			&event.EventHash,
			&event.TimestampMS,
			&event.Timestamp,
			&provider,
			&event.Model,
			&endpoint,
			&method,
			&path,
			&authType,
			&authIndex,
			&source,
			&sourceHash,
			&apiKeyHash,
			&accountSnapshot,
			&authLabelSnapshot,
			&authFileSnapshot,
			&authProviderSnapshot,
			&authProjectIDSnapshot,
			&authSnapshotAt,
			&requestedModel,
			&resolvedModel,
			&event.InputTokens,
			&event.OutputTokens,
			&event.ReasoningTokens,
			&event.CachedTokens,
			&event.CacheTokens,
			&event.TotalTokens,
			&latency,
			&failed,
			&rawJSON,
			&event.CreatedAtMS,
		); err != nil {
			return nil, err
		}
		event.RequestID = requestID.String
		event.Provider = provider.String
		event.Endpoint = endpoint.String
		event.Method = method.String
		event.Path = path.String
		event.AuthType = authType.String
		event.AuthIndex = authIndex.String
		event.Source = source.String
		event.SourceHash = sourceHash.String
		event.APIKeyHash = apiKeyHash.String
		event.AccountSnapshot = accountSnapshot.String
		event.AuthLabelSnapshot = authLabelSnapshot.String
		event.AuthFileSnapshot = authFileSnapshot.String
		event.AuthProviderSnapshot = authProviderSnapshot.String
		event.AuthProjectIDSnapshot = authProjectIDSnapshot.String
		event.RequestedModel = requestedModel.String
		event.ResolvedModel = resolvedModel.String
		if authSnapshotAt.Valid {
			event.AuthSnapshotAtMS = authSnapshotAt.Int64
		}
		event.RawJSON = rawJSON.String
		event.Failed = failed != 0
		if latency.Valid {
			value := latency.Int64
			event.LatencyMS = &value
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) Counts(ctx context.Context) (events int64, deadLetters int64, err error) {
	if err = s.db.QueryRowContext(ctx, `select count(*) from usage_events`).Scan(&events); err != nil {
		return 0, 0, err
	}
	if err = s.db.QueryRowContext(ctx, `select count(*) from dead_letter_events`).Scan(&deadLetters); err != nil {
		return 0, 0, err
	}
	return events, deadLetters, nil
}

func (s *Store) ExportJSONL(ctx context.Context) ([]byte, error) {
	events, err := s.RecentEvents(ctx, 0)
	if err != nil {
		return nil, err
	}
	output := make([]byte, 0)
	for i := len(events) - 1; i >= 0; i-- {
		line, err := json.Marshal(events[i])
		if err != nil {
			return nil, err
		}
		output = append(output, line...)
		output = append(output, '\n')
	}
	return output, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullPositiveInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func (s Setup) String() string {
	return fmt.Sprintf("upstream=%s queue=%s popSide=%s", s.CPAUpstreamURL, s.Queue, s.PopSide)
}

// ProviderAuthTotal holds aggregated success/failure counts for a single
// (provider, auth_index) pair stored in usage_events.
type ProviderAuthTotal struct {
	Provider  string
	AuthIndex string
	Success   int64
	Failed    int64
}

// ProviderAuthTotals returns per-(provider, auth_index) success/failure totals
// from the pre-aggregated usage_daily_stats table. Rows with 'unknown' or empty
// provider/auth_index are skipped. This enables the management API to return
// persistent provider stats in sub-millisecond time without full-table scans.
func (s *Store) ProviderAuthTotals(ctx context.Context) ([]ProviderAuthTotal, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	const q = `
		SELECT provider,
		       auth_index,
		       SUM(success_count) AS success,
		       SUM(failed_count) AS failed
		FROM usage_daily_stats
		WHERE auth_index != 'unknown'
		  AND auth_index != ''
		  AND provider != 'unknown'
		  AND provider != ''
		GROUP BY provider, auth_index`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProviderAuthTotal
	for rows.Next() {
		var t ProviderAuthTotal
		if err := rows.Scan(&t.Provider, &t.AuthIndex, &t.Success, &t.Failed); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PruneOldEvents cleans up raw detailed event logs older than maxAgeDays while preserving
// all historical aggregate stats in usage_daily_stats. It also triggers a passive WAL checkpoint
// to reclaim disk space.
func (s *Store) PruneOldEvents(ctx context.Context, maxAgeDays int) (int64, error) {
	if s == nil || s.db == nil || maxAgeDays <= 0 {
		return 0, nil
	}
	cutoffMS := time.Now().AddDate(0, 0, -maxAgeDays).UnixMilli()
	res, err := s.db.ExecContext(ctx, `delete from usage_events where timestamp_ms < ?`, cutoffMS)
	if err != nil {
		return 0, err
	}
	rowsDeleted, _ := res.RowsAffected()

	// Clean up old dead letter events too
	_, _ = s.db.ExecContext(ctx, `delete from dead_letter_events where created_at_ms < ?`, cutoffMS)

	// Passive WAL checkpoint to manage wal file size
	_, _ = s.db.ExecContext(ctx, `pragma wal_checkpoint(PASSIVE)`)

	if rowsDeleted > 0 {
		log.Infof("[usage-store] pruned %d raw usage events older than %d days (cutoff: %s)",
			rowsDeleted, maxAgeDays, time.UnixMilli(cutoffMS).Format(time.RFC3339))
	}
	return rowsDeleted, nil
}

// StartAutoPruner starts a background worker that periodically purges old raw events.
func (s *Store) StartAutoPruner(ctx context.Context, maxAgeDays int, interval time.Duration) {
	if s == nil || maxAgeDays <= 0 {
		return
	}
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// Initial run after 1 minute
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Minute):
			_, _ = s.PruneOldEvents(ctx, maxAgeDays)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.PruneOldEvents(ctx, maxAgeDays)
			}
		}
	}()
}
