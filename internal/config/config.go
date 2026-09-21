package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is one schema for every machine. The box fills in the daemon keys;
// a laptop fills in Server and Token and leaves the rest at their defaults.
type Config struct {
	Bind      string   `toml:"bind"`
	Server    string   `toml:"server"`
	Token     string   `toml:"token"`
	DB        string   `toml:"db"`
	BackupDir string   `toml:"backup_dir"`
	Mnemo     Mnemo    `toml:"mnemo"`
	Ollama    Ollama   `toml:"ollama"`
	Calendar  Calendar `toml:"calendar"`
}

type Mnemo struct {
	URL   string `toml:"url"`
	Token string `toml:"token"`
}

type Ollama struct {
	URL   string `toml:"url"`
	Model string `toml:"model"`
}

// Calendar is a published ICS feed, read and never written. Leaving it empty
// is a normal configuration: the scheduler then works from working hours
// alone, which already describes most of a week.
type Calendar struct {
	ICSURL string `toml:"ics_url"`
}

func Default() Config {
	return Config{
		Bind: "127.0.0.1:7930",
		Ollama: Ollama{
			URL:   "http://localhost:11434",
			Model: "qwen3.6:35b",
		},
	}
}

// ServerURL is where a client should look for the daemon. Falls back to bind
// so a machine running both needs no extra configuration.
func (c Config) ServerURL() string {
	if c.Server != "" {
		return strings.TrimSuffix(c.Server, "/")
	}
	return "http://" + c.Bind
}

// DBPath is where the SQLite file lives, defaulting next to the config file.
func (c Config) DBPath() (string, error) {
	if c.DB != "" {
		return c.DB, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving config dir: %w", err)
	}
	return filepath.Join(dir, "ordo", "ordo.db"), nil
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving config dir: %w", err)
	}
	return filepath.Join(dir, "ordo", "config.toml"), nil
}

func Load(path string) (Config, error) {
	cfg := Default()
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return cfg, fmt.Errorf("loading config %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return cfg, fmt.Errorf("loading config %s: unknown key %s", path, undecoded[0])
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("saving config %s: %w", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("saving config %s: %w", path, err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return fmt.Errorf("saving config %s: %w", path, err)
	}
	return nil
}

func NewToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
