//go:build !cluster

package store

import (
	"context"
	"errors"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type PostgresStoreConfig struct {
	DSN         string
	Schema      string
	ConfigTable string
	AuthTable   string
	SpoolDir    string
}

type PostgresStore struct{}

func NewPostgresStore(ctx context.Context, cfg PostgresStoreConfig) (*PostgresStore, error) {
	return nil, errors.New("postgres store not enabled in this build")
}

func (s *PostgresStore) Bootstrap(ctx context.Context, exampleConfigPath string) error {
	return nil
}

func (s *PostgresStore) ConfigPath() string {
	return ""
}

func (s *PostgresStore) AuthDir() string {
	return ""
}

func (s *PostgresStore) WorkDir() string {
	return ""
}

func (s *PostgresStore) Save(ctx context.Context, auth *cliproxyauth.Auth) (string, error) {
	return "", errors.New("postgres store not enabled")
}

func (s *PostgresStore) List(ctx context.Context) ([]*cliproxyauth.Auth, error) {
	return nil, errors.New("postgres store not enabled")
}

func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	return errors.New("postgres store not enabled")
}

type GitTokenStore struct{}

func NewGitTokenStore(remote, username, password, branch string) *GitTokenStore {
	return &GitTokenStore{}
}

func (s *GitTokenStore) SetBaseDir(dir string) {}

func (s *GitTokenStore) EnsureRepository() error {
	return errors.New("git store not enabled in this build")
}

func (s *GitTokenStore) ConfigPath() string {
	return ""
}

func (s *GitTokenStore) AuthDir() string {
	return ""
}

func (s *GitTokenStore) PersistConfig(ctx context.Context) error {
	return errors.New("git store not enabled")
}

func (s *GitTokenStore) Save(ctx context.Context, auth *cliproxyauth.Auth) (string, error) {
	return "", errors.New("git store not enabled")
}

func (s *GitTokenStore) List(ctx context.Context) ([]*cliproxyauth.Auth, error) {
	return nil, errors.New("git store not enabled")
}

func (s *GitTokenStore) Delete(ctx context.Context, id string) error {
	return errors.New("git store not enabled")
}

type ObjectStoreConfig struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	Prefix    string
	LocalRoot string
	UseSSL    bool
	PathStyle bool
}

type ObjectTokenStore struct{}

func NewObjectTokenStore(cfg ObjectStoreConfig) (*ObjectTokenStore, error) {
	return nil, errors.New("object store not enabled in this build")
}

func (s *ObjectTokenStore) Bootstrap(ctx context.Context, exampleConfigPath string) error {
	return nil
}

func (s *ObjectTokenStore) ConfigPath() string {
	return ""
}

func (s *ObjectTokenStore) AuthDir() string {
	return ""
}

func (s *ObjectTokenStore) Save(ctx context.Context, auth *cliproxyauth.Auth) (string, error) {
	return "", errors.New("object store not enabled")
}

func (s *ObjectTokenStore) List(ctx context.Context) ([]*cliproxyauth.Auth, error) {
	return nil, errors.New("object store not enabled")
}

func (s *ObjectTokenStore) Delete(ctx context.Context, id string) error {
	return errors.New("object store not enabled")
}
